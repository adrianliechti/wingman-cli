package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAndRemoveServerPreservesDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")

	if err := os.WriteFile(path, []byte(`{
  "$schema": "https://example.com/schema.json",
  "mcpServers": {
    "existing": {"command": "srv", "custom": true}
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	server := ServerConfig{URL: "https://example.com/mcp", Headers: map[string]string{"X-Key": "1"}, OAuth: &OAuthConfig{ClientID: "cid"}}
	if err := SaveServer(path, "remote", server); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc["$schema"]) != `"https://example.com/schema.json"` {
		t.Fatalf("$schema was not preserved: %s", data)
	}
	if !strings.Contains(string(data), `"custom": true`) {
		t.Fatalf("unknown server field was not preserved: %s", data)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Servers["remote"]; got.URL != server.URL || got.Headers["X-Key"] != "1" || got.OAuth == nil || got.OAuth.ClientID != "cid" {
		t.Fatalf("remote = %+v", got)
	}

	removed, err := RemoveServer(path, "existing")
	if err != nil || !removed {
		t.Fatalf("RemoveServer = %v, %v", removed, err)
	}
	removed, err = RemoveServer(path, "existing")
	if err != nil || removed {
		t.Fatalf("second RemoveServer = %v, %v", removed, err)
	}

	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Servers["existing"]; ok || len(cfg.Servers) != 1 {
		t.Fatalf("servers = %+v", cfg.Servers)
	}
}

func TestSaveServerCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "mcp.json")

	if err := SaveServer(path, "fs", ServerConfig{Command: "npx", Args: []string{"-y", "server"}, Env: map[string]string{"A": "1"}}); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Servers["fs"]; got.Command != "npx" || len(got.Args) != 2 || got.Env["A"] != "1" || got.OAuth != nil {
		t.Fatalf("fs = %+v", got)
	}
}
