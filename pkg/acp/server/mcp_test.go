package server

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

func TestRequestedMCPConfigConvertsStdio(t *testing.T) {
	servers, err := requestedMCPConfig([]acpsdk.McpServer{{Stdio: &acpsdk.McpServerStdio{
		Name: "files", Command: "server", Args: []string{"--stdio"},
		Env: []acpsdk.EnvVariable{{Name: "TOKEN", Value: "secret"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got := servers["files"]
	if got.Command != "server" || len(got.Args) != 1 || got.Args[0] != "--stdio" || got.Env["TOKEN"] != "secret" {
		t.Fatalf("stdio config = %#v", got)
	}
}

func TestRequestedMCPConfigConvertsRemoteTransports(t *testing.T) {
	servers, err := requestedMCPConfig([]acpsdk.McpServer{
		{Http: &acpsdk.McpServerHttpInline{
			Name: "remote", Url: "https://example.invalid/mcp",
			Headers: []acpsdk.HttpHeader{{Name: "Authorization", Value: "Bearer t"}},
		}},
		{Sse: &acpsdk.McpServerSseInline{Name: "events", Url: "https://example.invalid/sse"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := servers["remote"]; got.Transport != "streamable-http" || got.URL != "https://example.invalid/mcp" || got.Headers["Authorization"] != "Bearer t" {
		t.Fatalf("http config = %#v", got)
	}
	if got := servers["events"]; got.Transport != "sse" || got.URL != "https://example.invalid/sse" {
		t.Fatalf("sse config = %#v", got)
	}
}

func TestWorkspaceKeyIncludesRequestedMCPConfig(t *testing.T) {
	a, err := requestedMCPConfig([]acpsdk.McpServer{{Stdio: &acpsdk.McpServerStdio{Name: "a", Command: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := requestedMCPConfig([]acpsdk.McpServer{{Stdio: &acpsdk.McpServerStdio{Name: "a", Command: "two"}}})
	if err != nil {
		t.Fatal(err)
	}
	if workspaceKey("/workspace", nil) != "/workspace" {
		t.Fatal("empty MCP config changed the legacy workspace key")
	}
	if workspaceKey("/workspace", a) == workspaceKey("/workspace", b) {
		t.Fatal("different MCP configurations shared a workspace key")
	}
}

func TestMissingRequestedMCPServers(t *testing.T) {
	requested := map[string]mcp.ServerConfig{"available": {}, "missing": {}}
	connected := map[string]bool{"available": true, "unrelated": true}
	missing := missingRequestedMCPServers(requested, connected)
	if len(missing) != 1 || missing[0] != "missing" {
		t.Fatalf("missingRequestedMCPServers() = %q", missing)
	}
}
