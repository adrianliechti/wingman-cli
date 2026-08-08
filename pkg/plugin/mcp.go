package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

// MCPSchema is the canonical Agent Plugins 1.0.0 MCP configuration identifier.
const MCPSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

type mcpFile struct {
	Schema  string                     `json:"$schema"`
	Servers map[string]json.RawMessage `json:"mcpServers"`
}

type mcpServer struct {
	Type string `json:"type"`

	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Dir     string            `json:"cwd,omitempty"`

	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

var (
	stdioFields  = []string{"type", "command", "args", "env", "cwd"}
	remoteFields = []string{"type", "url", "headers"}
)

// parseMCP translates a plugin's mcp.json into Wingman server configs. A fatal
// error disables MCP for the plugin; per-server problems are returned as notes
// and skip only that entry.
func parseMCP(content []byte, manifestSchema, root, dataDir string) (map[string]mcp.ServerConfig, []string, error) {
	var file mcpFile

	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&file); err != nil {
		return nil, nil, fmt.Errorf("parse mcp.json: %w", err)
	}

	if file.Schema != MCPSchema {
		if file.Schema == "" {
			return nil, nil, fmt.Errorf("mcp.json is missing $schema; expected %s", MCPSchema)
		}
		return nil, nil, fmt.Errorf("unsupported MCP schema %q; expected %s", file.Schema, MCPSchema)
	}

	if manifestSchema != ManifestSchema {
		return nil, nil, fmt.Errorf("mcp.json targets a different Agent Plugins version than plugin.json")
	}

	var notes []string

	servers := make(map[string]mcp.ServerConfig, len(file.Servers))

	for _, name := range slices.Sorted(maps.Keys(file.Servers)) {
		server, err := parseMCPServer(file.Servers[name], root, dataDir)

		if err != nil {
			notes = append(notes, fmt.Sprintf("skipping MCP server %q: %v", name, err))
			continue
		}

		servers[name] = server
	}

	return servers, notes, nil
}

func parseMCPServer(raw json.RawMessage, root, data string) (mcp.ServerConfig, error) {
	var probe struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(raw, &probe); err != nil {
		return mcp.ServerConfig{}, fmt.Errorf("must be an object with a type: %w", err)
	}

	var allowed []string

	switch probe.Type {
	case "stdio":
		allowed = stdioFields
	case "streamable-http", "sse":
		allowed = remoteFields
	case "":
		return mcp.ServerConfig{}, fmt.Errorf("missing type")
	default:
		return mcp.ServerConfig{}, fmt.Errorf("unknown type %q", probe.Type)
	}

	if err := closedFields(raw, allowed); err != nil {
		return mcp.ServerConfig{}, err
	}

	var server mcpServer

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&server); err != nil {
		return mcp.ServerConfig{}, err
	}

	if probe.Type == "stdio" {
		return stdioServer(server, root, data)
	}

	return remoteServer(server)
}

func stdioServer(server mcpServer, root, data string) (mcp.ServerConfig, error) {
	command := server.Command

	switch {
	case bareCommand(command):
	case relativePath(command):
		resolved, err := resolveContained(filepath.FromSlash(strings.TrimPrefix(command, "./")), root, root)
		if err != nil {
			return mcp.ServerConfig{}, fmt.Errorf("command %w", err)
		}
		command = resolved
	default:
		return mcp.ServerConfig{}, fmt.Errorf("command must be a bare executable name or a ./ path inside the plugin")
	}

	env := make(map[string]string, len(server.Env)+2)

	for _, name := range slices.Sorted(maps.Keys(server.Env)) {
		if sameVariable(name, rootVariable) || sameVariable(name, dataVariable) {
			return mcp.ServerConfig{}, fmt.Errorf("env must not set the reserved variable %q", name)
		}

		if existing, ok := lookupVariable(env, name); ok {
			return mcp.ServerConfig{}, fmt.Errorf("env sets %q and %q, which name the same variable", existing, name)
		}

		env[name] = expand(server.Env[name], root, data)
	}

	args := make([]string, 0, len(server.Args))
	for _, arg := range server.Args {
		args = append(args, expand(arg, root, data))
	}

	dir, err := resolveDir(server.Dir, root, data)
	if err != nil {
		return mcp.ServerConfig{}, err
	}

	env[rootVariable] = root
	env[dataVariable] = data

	return mcp.ServerConfig{
		Transport: "stdio",

		Command: command,
		Args:    args,
		Env:     env,
		Dir:     dir,
	}, nil
}

// resolveDir validates the three cwd forms the spec allows and resolves each
// against the root it is anchored to. An omitted cwd means the plugin root.
func resolveDir(value, root, data string) (string, error) {
	if value == "" {
		return root, nil
	}

	if value == "./" {
		return resolveContained("", root, root)
	}

	if relativePath(value) {
		return resolveContained(filepath.FromSlash(strings.TrimPrefix(value, "./")), root, root)
	}

	for _, anchor := range []struct {
		placeholder string
		base        string
	}{
		{rootPlaceholder, root},
		{dataPlaceholder, data},
	} {
		if value == anchor.placeholder {
			return resolveContained("", anchor.base, anchor.base)
		}

		if suffix, ok := strings.CutPrefix(value, anchor.placeholder+"/"); ok {
			if strings.Contains(suffix, `\`) {
				return "", fmt.Errorf("cwd %q must use forward slashes", value)
			}
			return resolveContained(filepath.FromSlash(suffix), anchor.base, anchor.base)
		}
	}

	return "", fmt.Errorf("cwd must be a ./ path, ${PLUGIN_ROOT} or ${PLUGIN_DATA}")
}

func remoteServer(server mcpServer) (mcp.ServerConfig, error) {
	if err := validateURL(server.URL); err != nil {
		return mcp.ServerConfig{}, err
	}

	headers := make(map[string]string, len(server.Headers))
	canonical := make(map[string]string, len(server.Headers))

	for _, name := range slices.Sorted(maps.Keys(server.Headers)) {
		lower := strings.ToLower(name)

		if existing, ok := canonical[lower]; ok {
			return mcp.ServerConfig{}, fmt.Errorf("headers set %q and %q, which name the same header", existing, name)
		}
		canonical[lower] = name

		if !validHeaderName(name) {
			return mcp.ServerConfig{}, fmt.Errorf("invalid header name %q", name)
		}

		value := server.Headers[name]

		if !validHeaderValue(value) {
			return mcp.ServerConfig{}, fmt.Errorf("invalid value for header %q", name)
		}

		headers[name] = value
	}

	transport := "streamable-http"
	if server.Type == "sse" {
		transport = "sse"
	}

	return mcp.ServerConfig{
		Transport: transport,

		URL:     server.URL,
		Headers: headers,
	}, nil
}

func validateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url %q: %w", raw, err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url must be absolute HTTP or HTTPS")
	}

	if parsed.Host == "" {
		return fmt.Errorf("url must be absolute HTTP or HTTPS")
	}

	if parsed.User != nil {
		return fmt.Errorf("url must not contain user information")
	}

	if parsed.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("url must not contain a fragment")
	}

	if parsed.Scheme == "http" && !loopback(parsed.Hostname()) {
		return fmt.Errorf("non-loopback url must use HTTPS")
	}

	return nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}

	return false
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}

	const special = "!#$%&'*+-.^_`|~"

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune(special, r):
		default:
			return false
		}
	}

	return true
}

func validHeaderValue(value string) bool {
	for _, r := range value {
		if (r < 0x20 && r != '\t') || r == 0x7f {
			return false
		}
	}

	return true
}

// closedFields rejects any member that does not belong to the variant selected
// by "type", including a member that belongs to a different variant.
func closedFields(raw json.RawMessage, allowed []string) error {
	var fields map[string]json.RawMessage

	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}

	for _, field := range slices.Sorted(maps.Keys(fields)) {
		if !slices.Contains(allowed, field) {
			return fmt.Errorf("unexpected field %q", field)
		}

		if bytes.Equal(bytes.TrimSpace(fields[field]), []byte("null")) {
			return fmt.Errorf("field %q must use its declared type when present", field)
		}
	}

	return nil
}

func lookupVariable(env map[string]string, name string) (string, bool) {
	for existing := range env {
		if sameVariable(existing, name) {
			return existing, true
		}
	}

	return "", false
}
