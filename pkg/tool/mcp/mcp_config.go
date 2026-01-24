package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

type ServerConfig struct {
	Transport string `json:"transport,omitempty"`

	URL string `json:"url,omitempty"`

	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	Headers map[string]string `json:"headers,omitempty"`
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