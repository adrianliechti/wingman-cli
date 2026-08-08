package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "demo"
}`

func writePlugin(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	return root
}

func load(t *testing.T, files map[string]string) (*Plugin, []string, error) {
	t.Helper()

	root := writePlugin(t, files)
	return Load(root, t.TempDir())
}

func mustLoad(t *testing.T, files map[string]string) (*Plugin, []string) {
	t.Helper()

	p, notes, err := load(t, files)
	if err != nil {
		t.Fatalf("Load: %v (notes %v)", err, notes)
	}

	return p, notes
}

func hasNote(notes []string, substr string) bool {
	for _, note := range notes {
		if strings.Contains(note, substr) {
			return true
		}
	}

	return false
}

func TestLoadReadsSkillsAndServers(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json": validManifest,
		"skills/summarize/SKILL.md": `---
name: summarize
description: Summarize a report
---
# Summarize`,
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "deployment-api": {
      "type": "streamable-http",
      "url": "https://deploy.example.com/mcp",
      "headers": {"X-Tenant": "public"}
    }
  }
}`,
	})

	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none", notes)
	}

	if p.Name != "demo" {
		t.Fatalf("name = %q", p.Name)
	}

	if len(p.Skills) != 1 || p.Skills[0].Name != "summarize" {
		t.Fatalf("skills = %#v", p.Skills)
	}

	if p.Skills[0].Plugin != "demo" || p.Skills[0].Qualified() != "demo:summarize" {
		t.Fatalf("skill attribution = %#v", p.Skills[0])
	}

	server, ok := p.Servers["deployment-api"]
	if !ok {
		t.Fatalf("servers = %#v", p.Servers)
	}

	if server.Transport != "streamable-http" || server.URL != "https://deploy.example.com/mcp" {
		t.Fatalf("server = %#v", server)
	}

	if server.Headers["X-Tenant"] != "public" {
		t.Fatalf("headers = %#v", server.Headers)
	}
}

func TestLoadReportsAndIgnoresUnknownManifestField(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "demo",
  "commands": {"a": 1}
}`,
	})

	if !hasNote(notes, "commands") {
		t.Fatalf("notes = %v, want a report about the unknown field", notes)
	}

	if p.Name != "demo" {
		t.Fatalf("plugin should still load: %#v", p)
	}
}

func TestLoadReportsAndIgnoresNonObjectExtensions(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "demo",
  "extensions": "nope"
}`,
	})

	if !hasNote(notes, "extensions") {
		t.Fatalf("notes = %v, want a report about extensions", notes)
	}

	if p.Name != "demo" {
		t.Fatalf("plugin should still load: %#v", p)
	}
}

func TestLoadRejectsBadManifests(t *testing.T) {
	tests := map[string]string{
		"missing name":         `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"}`,
		"missing schema":       `{"name": "demo"}`,
		"wrong schema":         `{"$schema": "https://agent-plugins.org/schemas/2.0.0/plugin.schema.json", "name": "demo"}`,
		"uppercase name":       `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "Demo"}`,
		"leading hyphen":       `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "-demo"}`,
		"double hyphen":        `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "de--mo"}`,
		"double period":        `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "de..mo"}`,
		"wrong type":           `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "demo", "keywords": "a"}`,
		"unknown author field": `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "demo", "author": {"handle": "x"}}`,
		"non-object extension": `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "demo", "extensions": {"com.example": 1}}`,
		"not json":             `nope`,
	}

	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := load(t, map[string]string{"plugin.json": manifest}); err == nil {
				t.Fatalf("Load succeeded, want rejection")
			}
		})
	}
}

func TestLoadAcceptsFullManifest(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "acme.tools",
  "version": "1.2.0",
  "description": "Brief plugin description",
  "author": {"name": "Author", "email": "a@example.com", "url": "https://example.com"},
  "homepage": "https://docs.example.com/plugin",
  "repository": "https://github.com/example/plugin",
  "license": "MIT",
  "keywords": ["one", "two"],
  "extensions": {"com.example.client": {"setting": true}}
}`,
	})

	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none", notes)
	}

	if p.Name != "acme.tools" || p.Manifest.Version != "1.2.0" || p.Manifest.Author.Email != "a@example.com" {
		t.Fatalf("manifest = %#v", p.Manifest)
	}
}

func TestLoadDiscoversSkillsOneLevelDeepOnly(t *testing.T) {
	p, _ := mustLoad(t, map[string]string{
		"plugin.json": validManifest,
		"skills/top/SKILL.md": `---
name: top
description: top level
---
body`,
		"skills/nested/deeper/SKILL.md": `---
name: deeper
description: too deep
---
body`,
	})

	if len(p.Skills) != 1 || p.Skills[0].Name != "top" {
		t.Fatalf("skills = %#v, want only the immediate child", p.Skills)
	}
}

func TestLoadReportsSkillsNotADirectory(t *testing.T) {
	_, notes := mustLoad(t, map[string]string{
		"plugin.json": validManifest,
		"skills":      "not a directory",
	})

	if !hasNote(notes, "skills is not a directory") {
		t.Fatalf("notes = %v", notes)
	}
}

func TestLoadDisablesMCPOnSchemaMismatchButKeepsSkills(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json": validManifest,
		"skills/keep/SKILL.md": `---
name: keep
description: still loads
---
body`,
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/2.0.0/mcp.schema.json",
  "mcpServers": {}
}`,
	})

	if !hasNote(notes, "disabling MCP") {
		t.Fatalf("notes = %v", notes)
	}

	if len(p.Servers) != 0 {
		t.Fatalf("servers = %#v, want none", p.Servers)
	}

	if len(p.Skills) != 1 {
		t.Fatalf("skills = %#v, want the skill to survive", p.Skills)
	}
}

func TestLoadDisablesMCPOnUnknownTopLevelField(t *testing.T) {
	_, notes := mustLoad(t, map[string]string{
		"plugin.json": validManifest,
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {},
  "extra": true
}`,
	})

	if !hasNote(notes, "disabling MCP") {
		t.Fatalf("notes = %v", notes)
	}
}

func TestLoadSkipsOnlyTheInvalidServer(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json": validManifest,
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "good": {"type": "streamable-http", "url": "https://good.example/mcp"},
    "bad": {"type": "streamable-http", "url": "http://bad.example/mcp"},
    "mixed": {"type": "stdio", "command": "server", "url": "https://x.example/mcp"},
    "unknown": {"type": "carrier-pigeon"}
  }
}`,
	})

	if len(p.Servers) != 1 {
		t.Fatalf("servers = %#v, want only the valid one", p.Servers)
	}

	if _, ok := p.Servers["good"]; !ok {
		t.Fatalf("servers = %#v", p.Servers)
	}

	for _, name := range []string{"bad", "mixed", "unknown"} {
		if !hasNote(notes, `skipping MCP server "`+name+`"`) {
			t.Fatalf("notes = %v, want a report for %q", notes, name)
		}
	}
}

func TestStdioServerExpandsPlaceholders(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json":    validManifest,
		"bin/validator":  "#!/bin/sh\n",
		"config/db.json": "{}",
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "local": {
      "type": "stdio",
      "command": "./bin/validator",
      "args": ["--config", "${PLUGIN_ROOT}/config/db.json", "--data", "${PLUGIN_DATA}/state"],
      "env": {"CACHE": "${PLUGIN_DATA}/cache"},
      "cwd": "${PLUGIN_DATA}"
    }
  }
}`,
	})

	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none", notes)
	}

	server := p.Servers["local"]

	if server.Command != filepath.Join(p.Root, "bin", "validator") {
		t.Fatalf("command = %q, want the resolved bundled path", server.Command)
	}

	wantArgs := []string{"--config", p.Root + "/config/db.json", "--data", p.Data + "/state"}
	for i, want := range wantArgs {
		if server.Args[i] != want {
			t.Fatalf("args[%d] = %q, want %q", i, server.Args[i], want)
		}
	}

	if server.Env["CACHE"] != p.Data+"/cache" {
		t.Fatalf("env = %#v", server.Env)
	}

	if server.Env["PLUGIN_ROOT"] != p.Root || server.Env["PLUGIN_DATA"] != p.Data {
		t.Fatalf("reserved variables = %#v", server.Env)
	}

	if server.Dir != p.Data {
		t.Fatalf("dir = %q, want the data directory", server.Dir)
	}

	if _, err := os.Stat(p.Data); err != nil {
		t.Fatalf("data directory was not created: %v", err)
	}
}

func TestStdioServerDefaultsToPluginRoot(t *testing.T) {
	p, _ := mustLoad(t, map[string]string{
		"plugin.json": validManifest,
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {"local": {"type": "stdio", "command": "npx"}}
}`,
	})

	if server := p.Servers["local"]; server.Dir != p.Root {
		t.Fatalf("dir = %q, want the plugin root %q", server.Dir, p.Root)
	}
}

func TestStdioServerRejections(t *testing.T) {
	tests := map[string]string{
		"escaping command":  `{"type": "stdio", "command": "../bin/server"}`,
		"absolute command":  `{"type": "stdio", "command": "/usr/bin/server"}`,
		"nested path":       `{"type": "stdio", "command": "bin/server"}`,
		"escaping cwd":      `{"type": "stdio", "command": "npx", "cwd": "../elsewhere"}`,
		"bare cwd":          `{"type": "stdio", "command": "npx", "cwd": "data"}`,
		"escaping root cwd": `{"type": "stdio", "command": "npx", "cwd": "${PLUGIN_ROOT}/../out"}`,
		"escaping data cwd": `{"type": "stdio", "command": "npx", "cwd": "${PLUGIN_DATA}/../out"}`,
		"reserved root env": `{"type": "stdio", "command": "npx", "env": {"PLUGIN_ROOT": "/tmp"}}`,
		"reserved data env": `{"type": "stdio", "command": "npx", "env": {"PLUGIN_DATA": "/tmp"}}`,
		"null cwd":          `{"type": "stdio", "command": "npx", "cwd": null}`,
		"remote field":      `{"type": "stdio", "command": "npx", "headers": {}}`,
	}

	for name, server := range tests {
		t.Run(name, func(t *testing.T) {
			p, notes := mustLoad(t, map[string]string{
				"plugin.json": validManifest,
				"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {"local": ` + server + `}
}`,
			})

			if len(p.Servers) != 0 {
				t.Fatalf("servers = %#v, want the entry skipped", p.Servers)
			}

			if !hasNote(notes, "skipping MCP server") {
				t.Fatalf("notes = %v, want a report", notes)
			}
		})
	}
}

func TestRemoteServerRejections(t *testing.T) {
	tests := map[string]string{
		"plain http":       `{"type": "streamable-http", "url": "http://remote.example/mcp"}`,
		"relative url":     `{"type": "streamable-http", "url": "/mcp"}`,
		"user info":        `{"type": "streamable-http", "url": "https://user:pw@x.example/mcp"}`,
		"fragment":         `{"type": "streamable-http", "url": "https://x.example/mcp#frag"}`,
		"bad scheme":       `{"type": "streamable-http", "url": "ftp://x.example/mcp"}`,
		"duplicate header": `{"type": "streamable-http", "url": "https://x.example/mcp", "headers": {"X-A": "1", "x-a": "2"}}`,
		"bad header name":  `{"type": "streamable-http", "url": "https://x.example/mcp", "headers": {"X A": "1"}}`,
		"stdio field":      `{"type": "streamable-http", "url": "https://x.example/mcp", "command": "npx"}`,
	}

	for name, server := range tests {
		t.Run(name, func(t *testing.T) {
			p, notes := mustLoad(t, map[string]string{
				"plugin.json": validManifest,
				"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {"remote": ` + server + `}
}`,
			})

			if len(p.Servers) != 0 {
				t.Fatalf("servers = %#v, want the entry skipped", p.Servers)
			}

			if !hasNote(notes, "skipping MCP server") {
				t.Fatalf("notes = %v, want a report", notes)
			}
		})
	}
}

func TestRemoteServerAllowsLoopbackHTTP(t *testing.T) {
	for _, url := range []string{"http://localhost:8080/mcp", "http://127.0.0.1:8080/mcp", "http://[::1]:8080/mcp"} {
		p, notes := mustLoad(t, map[string]string{
			"plugin.json": validManifest,
			"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {"local": {"type": "streamable-http", "url": "` + url + `"}}
}`,
		})

		if len(p.Servers) != 1 {
			t.Fatalf("%s: servers = %#v, notes = %v", url, p.Servers, notes)
		}
	}
}

func TestLoadRejectsSymlinkedCommandEscapingRoot(t *testing.T) {
	outside := t.TempDir()

	target := filepath.Join(outside, "server")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	root := writePlugin(t, map[string]string{
		"plugin.json": validManifest,
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {"local": {"type": "stdio", "command": "./bin/server"}}
}`,
	})

	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	if err := os.Symlink(target, filepath.Join(root, "bin", "server")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	p, notes, err := Load(root, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(p.Servers) != 0 {
		t.Fatalf("servers = %#v, want the escaping command rejected", p.Servers)
	}

	if !hasNote(notes, "skipping MCP server") {
		t.Fatalf("notes = %v, want a report", notes)
	}
}

func TestLoadAcceptsSymlinkResolvingInsideRoot(t *testing.T) {
	root := writePlugin(t, map[string]string{
		"plugin.json": validManifest,
		"real/server": "#!/bin/sh\n",
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {"local": {"type": "stdio", "command": "./bin/server"}}
}`,
	})

	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	if err := os.Symlink(filepath.Join(root, "real", "server"), filepath.Join(root, "bin", "server")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	p, notes, err := Load(root, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(p.Servers) != 1 {
		t.Fatalf("servers = %#v, notes = %v, want the server kept", p.Servers, notes)
	}
}

func TestSSEServerKeepsItsTransport(t *testing.T) {
	p, _ := mustLoad(t, map[string]string{
		"plugin.json": validManifest,
		"mcp.json": `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {"legacy": {"type": "sse", "url": "https://legacy.example.com/sse"}}
}`,
	})

	if server := p.Servers["legacy"]; server.Transport != "sse" {
		t.Fatalf("transport = %q, want sse", server.Transport)
	}
}
