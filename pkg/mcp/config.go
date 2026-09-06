package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

type ServerConfig struct {
	Transport string `json:"transport,omitempty"`

	URL string `json:"url,omitempty"`

	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	Headers map[string]string `json:"headers,omitempty"`

	OAuth *OAuthConfig `json:"oauth,omitempty"`

	// Dir overrides the manager-wide working directory for this server alone.
	// Plugin-provided servers run in their own package or data directory.
	Dir string `json:"-"`
}

// OAuthConfig tunes the authorization code flow for a remote server. Without
// it, Wingman registers itself dynamically and listens on the default
// callback port.
type OAuthConfig struct {
	ClientID     string `json:"clientId,omitempty"`
	CallbackPort int    `json:"callbackPort,omitempty"`
}

// GlobalConfigPath is the user-wide mcp.json shared by all projects.
func GlobalConfigPath() string {
	path, _ := layout.WingmanPath("mcp.json")
	return path
}

// LoadConfig reads one mcp.json. A missing file yields os.ErrNotExist.
func LoadConfig(path string) (*Config, error) {
	return loadConfig(path)
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)

	if err != nil {
		return nil, fmt.Errorf("failed to read mcp.json: %w", err)
	}

	var cfg Config

	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse mcp.json: %w", err)
	}

	return &cfg, nil
}

// SaveServer writes server under name into the mcp.json at path, creating the
// file if needed. Other keys and servers in the file are preserved verbatim.
func SaveServer(path, name string, server ServerConfig) error {
	doc, servers, err := readDocument(path)

	if err != nil {
		return err
	}

	data, err := json.Marshal(server)

	if err != nil {
		return err
	}

	servers[name] = data

	return writeDocument(path, doc, servers)
}

// RemoveServer deletes name from the mcp.json at path and reports whether it
// was present.
func RemoveServer(path, name string) (bool, error) {
	doc, servers, err := readDocument(path)

	if err != nil {
		return false, err
	}

	if _, ok := servers[name]; !ok {
		return false, nil
	}

	delete(servers, name)

	return true, writeDocument(path, doc, servers)
}

func readDocument(path string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	doc := map[string]json.RawMessage{}
	servers := map[string]json.RawMessage{}

	data, err := os.ReadFile(path)

	if errors.Is(err, os.ErrNotExist) {
		return doc, servers, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("failed to read mcp.json: %w", err)
	}

	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("failed to parse mcp.json: %w", err)
	}

	if raw, ok := doc["mcpServers"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, fmt.Errorf("failed to parse mcp.json: %w", err)
		}
	}

	return doc, servers, nil
}

func writeDocument(path string, doc, servers map[string]json.RawMessage) error {
	raw, err := json.Marshal(servers)

	if err != nil {
		return err
	}

	doc["mcpServers"] = raw

	data, err := json.MarshalIndent(doc, "", "  ")

	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}
