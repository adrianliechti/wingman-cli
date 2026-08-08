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

func TestLoadIgnoresUnsupportedExtensionValues(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json": `{
  "$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name":"demo",
  "extensions":{"com.example.flag":true,"org.example.count":3}
}`,
	})
	if p.Name != "demo" || len(notes) != 0 {
		t.Fatalf("plugin = %#v, notes = %v", p, notes)
	}
}

func TestLoadRejectsNullOptionalManifestFields(t *testing.T) {
	for _, field := range []string{"version", "description", "author", "homepage", "repository", "license", "keywords"} {
		t.Run(field, func(t *testing.T) {
			manifest := `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"demo","` + field + `":null}`
			if _, _, err := load(t, map[string]string{"plugin.json": manifest}); err == nil {
				t.Fatalf("null %s was accepted", field)
			}
		})
	}
}

func TestLoadRejectsNullAuthorFields(t *testing.T) {
	for _, field := range []string{"name", "email", "url"} {
		t.Run(field, func(t *testing.T) {
			manifest := `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"demo","author":{"` + field + `":null}}`
			if _, _, err := load(t, map[string]string{"plugin.json": manifest}); err == nil || !strings.Contains(err.Error(), "author."+field) {
				t.Fatalf("error = %v", err)
			}
		})
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

func TestLoadDisablesPortableMCPWithoutServerObject(t *testing.T) {
	for name, body := range map[string]string{
		"missing": `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"}`,
		"null":    `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			p, notes := mustLoad(t, map[string]string{"plugin.json": validManifest, "mcp.json": body})
			if len(p.Servers) != 0 || !hasNote(notes, "required and must be an object") {
				t.Fatalf("servers = %#v, notes = %v", p.Servers, notes)
			}
		})
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

func TestLoadAcceptsMCPFileSymlinkResolvingInsideRoot(t *testing.T) {
	root := writePlugin(t, map[string]string{
		"plugin.json": validManifest,
		"config/portable.json": `{
  "$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers":{"remote":{"type":"streamable-http","url":"https://example.test/mcp"}}
}`,
	})
	if err := os.Symlink(filepath.Join(root, "config", "portable.json"), filepath.Join(root, "mcp.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	p, notes, err := Load(root, t.TempDir())
	if err != nil || len(p.Servers) != 1 || len(notes) != 0 {
		t.Fatalf("servers = %#v, notes = %v, err = %v", p.Servers, notes, err)
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

func TestLoadCodexPluginDefaultHooksAndRecursiveSkills(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".codex-plugin/plugin.json": `{"name":"codex-demo"}`,
		"hooks/hooks.json":          `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
		"skills/group/deploy/SKILL.md": `---
name: deploy
description: Deploy the service
---
Deploy it.`,
	})

	if len(notes) != 0 {
		t.Fatalf("notes = %v", notes)
	}
	if p.Format != CodexPluginFormat || p.Name != "codex-demo" {
		t.Fatalf("plugin = %#v", p)
	}
	if p.Hooks == nil || p.Hooks.RuleCount() != 1 {
		t.Fatalf("hooks = %#v", p.Hooks)
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "deploy" {
		t.Fatalf("skills = %#v", p.Skills)
	}
	if _, err := os.Stat(p.Data); err != nil {
		t.Fatalf("plugin data directory = %q: %v", p.Data, err)
	}
}

func TestLoadCodexPluginRootSkillAndDefaultMCP(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".codex-plugin/plugin.json": `{"name":"codex-demo"}`,
		"skills/SKILL.md": `---
name: sample
description: Root skill accepted by Codex
---
Use it.`,
		".mcp.json": `{"mcpServers":{
  "docs":{"type":"http","url":"https://docs.example/mcp","headers":{"X-Plugin":"demo"}},
  "local":{"command":"node","args":["${PLUGIN_ROOT}/server.js"],"cwd":"scripts"}
}}`,
		"scripts/.keep": "",
	})
	if len(notes) != 0 {
		t.Fatalf("notes = %v", notes)
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "sample" {
		t.Fatalf("skills = %#v", p.Skills)
	}
	if p.Servers["docs"].Transport != "streamable-http" || p.Servers["docs"].Headers["X-Plugin"] != "demo" {
		t.Fatalf("docs server = %#v", p.Servers["docs"])
	}
	local := p.Servers["local"]
	if len(local.Args) != 1 || local.Args[0] != p.Root+"/server.js" || local.Dir != filepath.Join(p.Root, "scripts") {
		t.Fatalf("local server = %#v", local)
	}
}

func TestLoadCodexPluginDeclaredMCPForms(t *testing.T) {
	tests := map[string]struct {
		declaration string
		files       map[string]string
	}{
		"path": {
			declaration: `"./config/custom.json"`,
			files: map[string]string{
				"config/custom.json": `{"mcpServers":{"custom":{"type":"sse","url":"https://example.test/sse"}}}`,
				".mcp.json":          `{"mcpServers":{"default":{"url":"https://ignored.test/mcp"}}}`,
			},
		},
		"inline": {
			declaration: `{"inline":{"type":"http","url":"https://example.test/mcp"}}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{".codex-plugin/plugin.json": `{"name":"demo","mcpServers":` + test.declaration + `}`}
			for path, content := range test.files {
				files[path] = content
			}
			p, notes := mustLoad(t, files)
			if len(notes) != 0 || len(p.Servers) != 1 {
				t.Fatalf("servers = %#v, notes = %v", p.Servers, notes)
			}
		})
	}
}

func TestLoadCodexPluginHookDeclarationForms(t *testing.T) {
	tests := map[string]string{
		"path":        `"./hooks/one.json"`,
		"paths":       `["./hooks/one.json","./hooks/two.json"]`,
		"inline":      `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
		"inline list": `[{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}},{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"true"}]}]}}]`,
	}

	for name, declaration := range tests {
		t.Run(name, func(t *testing.T) {
			p, notes := mustLoad(t, map[string]string{
				".codex-plugin/plugin.json": `{"name":"demo","hooks":` + declaration + `}`,
				"hooks/hooks.json":          `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"false"}]}]}}`,
				"hooks/one.json":            `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
				"hooks/two.json":            `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
			})
			if len(notes) != 0 {
				t.Fatalf("notes = %v", notes)
			}
			want := 1
			if name == "paths" || name == "inline list" {
				want = 2
			}
			if p.Hooks.RuleCount() != want {
				t.Fatalf("hook rules = %d, want %d", p.Hooks.RuleCount(), want)
			}
			if len(p.Hooks.Hooks.PreToolUse) != 0 {
				t.Fatal("manifest hooks must replace the default hooks/hooks.json")
			}
		})
	}
}

func TestLoadAgentPluginComOpenAIHooksExtension(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json": `{
  "$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name":"portable-demo",
  "extensions":{
    "com.openai":{
      "hooks":{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}
    }
  }
}`,
		"hooks/hooks.json": `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"false"}]}]}}`,
	})

	if len(notes) != 0 {
		t.Fatalf("notes = %v", notes)
	}
	if p.Format != AgentPluginFormat || p.Hooks.RuleCount() != 1 || len(p.Hooks.Hooks.SessionStart) != 1 {
		t.Fatalf("plugin hooks = %#v", p.Hooks)
	}
}

func TestLoadCodexPluginRejectsEscapingHookPath(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".codex-plugin/plugin.json": `{"name":"demo","hooks":"./hooks/../../outside.json"}`,
	})

	if p.Hooks.RuleCount() != 0 || !hasNote(notes, "must not contain ..") {
		t.Fatalf("hooks = %#v, notes = %v", p.Hooks, notes)
	}
}

func TestLoadClaudePluginInlineEventMap(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".claude-plugin/plugin.json": `{
  "name":"claude-demo",
  "hooks":{
    "SessionStart":[{"hooks":[{"type":"command","command":"true"}]}],
    "UserPromptSubmit":[{"hooks":[{"type":"command","command":"true"}]}]
  }
}`,
	})

	if p.Format != ClaudePluginFormat || len(notes) != 0 || p.Hooks.RuleCount() != 2 {
		t.Fatalf("hooks = %#v, notes = %v", p.Hooks, notes)
	}
}

func TestLoadClaudePluginUsesPermissiveSkillProfile(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-demo"}`,
		"skills/review/SKILL.md": `---
when_to_use: Use after editing code.
allowed-tools: [Read, Grep]
arguments: [target]
disable-model-invocation: true
---
# Review

Review $target carefully.`,
	})
	if len(notes) != 0 || len(p.Skills) != 1 {
		t.Fatalf("skills = %#v, notes = %v", p.Skills, notes)
	}
	sk := p.Skills[0]
	if sk.Name != "review" || !sk.DisableModelInvocation || len(sk.AllowedTools) != 2 || sk.Description != "Review $target carefully. Use after editing code." {
		t.Fatalf("skill = %#v", sk)
	}
	content, err := sk.GetContent("")
	if err != nil || content != "# Review\n\nReview $target carefully." {
		t.Fatalf("content = %q, err = %v", content, err)
	}
	if got := sk.ApplyArguments(content, "pkg/plugin", p.Root); !strings.Contains(got, "Review pkg/plugin carefully.") {
		t.Fatalf("arguments were not expanded: %q", got)
	}
}

func TestLoadClaudePluginFrontmatterNameControlsCommand(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-demo"}`,
		"skills/review/SKILL.md":     "---\nname: fancy-review\ndescription: review\n---\nbody",
	})
	if len(notes) != 0 || len(p.Skills) != 1 || p.Skills[0].Name != "fancy-review" || p.Skills[0].Qualified() != "claude-demo:fancy-review" {
		t.Fatalf("skills = %#v, notes = %v", p.Skills, notes)
	}
}

func TestLoadClaudePluginAddsCustomSkillsToDefaults(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".claude-plugin/plugin.json":    `{"name":"claude-demo","skills":"./custom-skills"}`,
		"skills/default/SKILL.md":       "---\ndescription: default\n---\ndefault body",
		"custom-skills/custom/SKILL.md": "---\ndescription: custom\n---\ncustom body",
	})
	if len(notes) != 0 || len(p.Skills) != 2 || p.Skills[0].Name != "default" || p.Skills[1].Name != "custom" {
		t.Fatalf("skills = %#v, notes = %v", p.Skills, notes)
	}
}

func TestLoadClaudeSingleSkillPlugin(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-demo"}`,
		"SKILL.md":                   "---\nname: root-command\ndescription: root skill\n---\nroot body",
	})
	if len(notes) != 0 || len(p.Skills) != 1 || p.Skills[0].Name != "root-command" {
		t.Fatalf("skills = %#v, notes = %v", p.Skills, notes)
	}
}

func TestLoadManifestlessClaudePlugin(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"skills/hello/SKILL.md": "---\ndescription: says hello\n---\nhello",
		"hooks/hooks.json":      `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
	})
	if p.Format != ClaudePluginFormat || len(notes) != 0 || len(p.Skills) != 1 || p.Hooks.RuleCount() != 1 {
		t.Fatalf("plugin = %#v, notes = %v", p, notes)
	}
}

func TestLoadClaudePluginMergesDeclaredMCPFiles(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_TOKEN", "secret")
	p, notes := mustLoad(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-demo","mcpServers":["./mcp/one.json","./mcp/two.json"]}`,
		"mcp/one.json":               `{"mcpServers":{"one":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/one.js"],"env":{"TOKEN":"${CLAUDE_PLUGIN_TOKEN}"}}}}`,
		"mcp/two.json":               `{"mcpServers":{"two":{"type":"http","url":"https://example.test/${CLAUDE_PLUGIN_TOKEN}","headers":{"Authorization":"Bearer ${CLAUDE_PLUGIN_TOKEN}"}}}}`,
	})
	if len(notes) != 0 || len(p.Servers) != 2 {
		t.Fatalf("servers = %#v, notes = %v", p.Servers, notes)
	}
	if p.Servers["one"].Env["TOKEN"] != "secret" || p.Servers["one"].Args[0] != p.Root+"/one.js" {
		t.Fatalf("stdio server = %#v", p.Servers["one"])
	}
	if p.Servers["two"].URL != "https://example.test/secret" || p.Servers["two"].Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("HTTP server = %#v", p.Servers["two"])
	}
}

func TestLoadClaudeMCPUsesEnvironmentDefaults(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-demo"}`,
		".mcp.json":                  `{"mcpServers":{"remote":{"type":"http","url":"${MISSING_MCP_URL:-https://example.test/mcp}"}}}`,
	})
	if len(notes) != 0 || p.Servers["remote"].URL != "https://example.test/mcp" {
		t.Fatalf("servers = %#v, notes = %v", p.Servers, notes)
	}
}

func TestLoadClaudeMCPRequiresRemoteType(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-demo"}`,
		".mcp.json":                  `{"mcpServers":{"remote":{"url":"https://example.test/mcp"}}}`,
	})
	if len(p.Servers) != 0 || !hasNote(notes, "needs type") {
		t.Fatalf("servers = %#v, notes = %v", p.Servers, notes)
	}
}

func TestLoadNativeMCPSkipsUnsupportedBehaviorFields(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".codex-plugin/plugin.json": `{"name":"demo"}`,
		".mcp.json":                 `{"mcpServers":{"secure":{"type":"http","url":"https://example.test/mcp","oauth":{"clientId":"demo"}}}}`,
	})
	if len(p.Servers) != 0 || !hasNote(notes, `unsupported field "oauth"`) {
		t.Fatalf("servers = %#v, notes = %v", p.Servers, notes)
	}
}

func TestLoadAgentPluginDoesNotImplicitlyEnableNativeHooks(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		"plugin.json":      validManifest,
		"hooks/hooks.json": `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"false"}]}]}}`,
	})
	if len(notes) != 0 || p.Hooks.RuleCount() != 0 {
		t.Fatalf("hooks = %#v, notes = %v", p.Hooks, notes)
	}
}

func TestExplicitInvalidHooksNeverFallBackToDefault(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".codex-plugin/plugin.json": `{"name":"demo","hooks":"./hooks/missing.json"}`,
		"hooks/hooks.json":          `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"false"}]}]}}`,
	})
	if p.Hooks.RuleCount() != 0 || !hasNote(notes, "missing.json") {
		t.Fatalf("hooks = %#v, notes = %v", p.Hooks, notes)
	}
}

func TestCursorManifestIsNotAPlugin(t *testing.T) {
	if _, _, err := load(t, map[string]string{".cursor-plugin/plugin.json": `{"name":"cursor"}`}); err == nil {
		t.Fatal("Cursor manifest unexpectedly loaded")
	}
}

func TestLoadCodexPluginReportsMissingDeclaredHookFile(t *testing.T) {
	p, notes := mustLoad(t, map[string]string{
		".codex-plugin/plugin.json": `{"name":"demo","hooks":"./hooks/missing.json"}`,
	})
	if p.Hooks.RuleCount() != 0 || !hasNote(notes, "missing.json") {
		t.Fatalf("hooks = %#v, notes = %v", p.Hooks, notes)
	}
}

func TestLoadRejectsLegacyNameEscapingDataRoot(t *testing.T) {
	_, _, err := load(t, map[string]string{
		".codex-plugin/plugin.json": `{"name":"../escape"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "escapes the plugin data root") {
		t.Fatalf("error = %v", err)
	}
}
