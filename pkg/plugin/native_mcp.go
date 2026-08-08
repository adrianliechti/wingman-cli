package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

// nativeMCPServer is the common Codex/Claude .mcp.json subset Wingman can run.
// Both clients accept a few spelling variants, so normalization happens here
// instead of leaking compatibility aliases into the runtime MCP config.
type nativeMCPServer struct {
	Type      string            `json:"type,omitempty"`
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	HTTPHeads map[string]string `json:"http_headers,omitempty"`
}

func loadNativeServers(p *Plugin, manifest resolvedManifest) (map[string]mcp.ServerConfig, []string) {
	type source struct {
		name         string
		content      []byte
		allowWrapper bool
	}
	var sources []source
	var notes []string
	if manifest.MCPPresent && isObject(manifest.MCP) {
		sources = append(sources, source{manifest.Path + "#mcpServers", manifest.MCP, false})
	} else {
		paths := []string{"./.mcp.json"}
		if manifest.MCPPresent {
			var err error
			paths, err = manifestPaths(manifest.MCP)
			if err != nil || len(paths) == 0 || (manifest.Format != ClaudePluginFormat && len(paths) != 1) {
				return nil, []string{"disabling MCP: mcpServers must be ./ path(s) or an object"}
			}
		}
		for _, value := range paths {
			path, err := resolveManifestPath(p.Root, "mcpServers", value)
			if err != nil {
				notes = append(notes, "skipping MCP source: "+err.Error())
				continue
			}
			resolved, err := resolveExistingFile(p.Root, path)
			if err != nil {
				if os.IsNotExist(err) && !manifest.MCPPresent {
					continue
				}
				notes = append(notes, "skipping MCP source: "+err.Error())
				continue
			}
			content, err := os.ReadFile(resolved)
			if err != nil {
				notes = append(notes, fmt.Sprintf("skipping MCP source: %v", err))
				continue
			}
			sources = append(sources, source{resolved, content, true})
		}
	}

	servers := make(map[string]mcp.ServerConfig)
	for _, source := range sources {
		parsed, sourceNotes, err := parseNativeMCP(source.content, p.Root, p.Data, source.allowWrapper, manifest.Format)
		notes = append(notes, sourceNotes...)
		if err != nil {
			notes = append(notes, fmt.Sprintf("skipping MCP source %s: %v", source.name, err))
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(parsed)) {
			if _, exists := servers[name]; exists {
				notes = append(notes, fmt.Sprintf("skipping duplicate MCP server %q from %s", name, source.name))
				continue
			}
			servers[name] = parsed[name]
		}
	}
	if needsData(servers) && p.Data != "" {
		if err := os.MkdirAll(p.Data, 0755); err != nil {
			return nil, append(notes, fmt.Sprintf("disabling MCP: create data directory: %v", err))
		}
	}
	return servers, notes
}

func resolveExistingFile(root, path string) (string, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return "", err
	}
	if !contains(root, resolved) {
		return "", fmt.Errorf("%s resolves outside the plugin root", filepath.Base(path))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	return resolved, nil
}

func parseNativeMCP(content []byte, root, data string, allowWrapper bool, format Format) (map[string]mcp.ServerConfig, []string, error) {
	var top map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&top); err != nil {
		return nil, nil, fmt.Errorf("parse native MCP config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, nil, fmt.Errorf("parse native MCP config: trailing JSON value")
		}
		return nil, nil, fmt.Errorf("parse native MCP config: %w", err)
	}

	serversRaw := top
	if wrapped, ok := top["mcpServers"]; allowWrapper && ok {
		if !isObject(wrapped) {
			return nil, nil, fmt.Errorf("mcpServers must be an object")
		}
		var unwrapped map[string]json.RawMessage
		if err := json.Unmarshal(wrapped, &unwrapped); err != nil {
			return nil, nil, fmt.Errorf("parse mcpServers: %w", err)
		}
		serversRaw = unwrapped
	}

	servers := make(map[string]mcp.ServerConfig, len(serversRaw))
	var notes []string
	for _, name := range slices.Sorted(maps.Keys(serversRaw)) {
		if strings.TrimSpace(name) == "" {
			notes = append(notes, "skipping MCP server with an empty name")
			continue
		}
		server, err := parseNativeMCPServer(serversRaw[name], root, data, format)
		if err != nil {
			notes = append(notes, fmt.Sprintf("skipping MCP server %q: %v", name, err))
			continue
		}
		servers[name] = server
	}
	return servers, notes, nil
}

func parseNativeMCPServer(raw json.RawMessage, root, data string, format Format) (mcp.ServerConfig, error) {
	if !isObject(raw) {
		return mcp.ServerConfig{}, fmt.Errorf("configuration must be an object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return mcp.ServerConfig{}, err
	}
	// These settings materially change authentication, enablement, timeouts, or
	// tool exposure. Silently dropping them would run a different server policy
	// than the plugin author declared, so skip that server with a diagnostic.
	for _, name := range []string{
		"oauth", "oauth_resource", "oauthResource", "headersHelper",
		"bearer_token_env_var", "env_http_headers", "enabled", "required",
		"enabled_tools", "disabled_tools", "tools", "timeout",
		"startup_timeout_sec", "tool_timeout_sec",
	} {
		if _, ok := fields[name]; ok {
			return mcp.ServerConfig{}, fmt.Errorf("unsupported field %q", name)
		}
	}
	var native nativeMCPServer
	if err := json.Unmarshal(raw, &native); err != nil {
		return mcp.ServerConfig{}, err
	}

	expand := func(value string) string { return expandNative(value, root, data, format) }
	if native.Command != "" {
		if native.URL != "" {
			return mcp.ServerConfig{}, fmt.Errorf("configuration must not set both command and url")
		}
		args := make([]string, len(native.Args))
		for i, arg := range native.Args {
			args[i] = expand(arg)
		}
		env := make(map[string]string, len(native.Env)+4)
		for name, value := range native.Env {
			env[name] = expand(value)
		}
		env[rootVariable] = root
		env["CLAUDE_PLUGIN_ROOT"] = root
		if data != "" {
			env[dataVariable] = data
			env["CLAUDE_PLUGIN_DATA"] = data
		}

		dir := root
		if native.Cwd != "" {
			value := expand(native.Cwd)
			if !filepath.IsAbs(value) {
				value = filepath.Join(root, value)
			}
			resolved, err := resolvePath(value)
			if err != nil || !contains(root, resolved) {
				return mcp.ServerConfig{}, fmt.Errorf("cwd must resolve inside the plugin root")
			}
			dir = resolved
		}
		return mcp.ServerConfig{
			Transport: "stdio",
			Command:   expand(native.Command),
			Args:      args,
			Env:       env,
			Dir:       dir,
		}, nil
	}

	if native.URL == "" {
		return mcp.ServerConfig{}, fmt.Errorf("configuration needs command or url")
	}
	if format == ClaudePluginFormat && native.Type == "" && native.Transport == "" {
		return mcp.ServerConfig{}, fmt.Errorf("Claude remote server needs type http, sse, or ws")
	}
	transport := native.Transport
	if transport == "" {
		transport = native.Type
	}
	switch transport {
	case "", "http", "streamable_http", "streamable-http":
		transport = "streamable-http"
	case "sse":
	default:
		return mcp.ServerConfig{}, fmt.Errorf("unknown transport %q", transport)
	}
	headers := make(map[string]string, len(native.HTTPHeads)+len(native.Headers))
	for name, value := range native.HTTPHeads {
		headers[name] = expand(value)
	}
	for name, value := range native.Headers {
		headers[name] = expand(value)
	}
	return mcp.ServerConfig{
		Transport: transport,
		URL:       expand(native.URL),
		Headers:   headers,
	}, nil
}

var nativePlaceholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

func expandNative(value, root, data string, format Format) string {
	return nativePlaceholderPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := nativePlaceholderPattern.FindStringSubmatch(match)
		name := parts[1]
		switch name {
		case rootVariable, "CLAUDE_PLUGIN_ROOT":
			return root
		case dataVariable, "CLAUDE_PLUGIN_DATA":
			return data
		default:
			if format == ClaudePluginFormat {
				if value := os.Getenv(name); value != "" {
					return value
				}
				return parts[3]
			}
			return match
		}
	})
}
