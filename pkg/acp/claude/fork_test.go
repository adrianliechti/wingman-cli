package claude

import (
	"context"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestForkSessionPreservesRequestedConfiguration(t *testing.T) {
	a := New(Options{})
	cwd := t.TempDir()
	additional := t.TempDir()
	server := acp.UnstableMcpServer{Http: &acp.UnstableMcpServerHttp{
		Name: "remote", Type: "http", Url: "https://example.test/mcp",
	}}

	resp, err := a.UnstableForkSession(context.Background(), acp.UnstableForkSessionRequest{
		SessionId:             "source-session",
		Cwd:                   cwd,
		AdditionalDirectories: []string{additional},
		McpServers:            []acp.UnstableMcpServer{server},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := a.lookup(resp.SessionId)
	if s == nil {
		t.Fatal("forked session was not registered")
	}
	s.mu.Lock()
	args := s.cliArgsLocked()
	mcpCount := len(s.mcpServers)
	resumeFrom, forkOnResume := s.resumeFrom, s.forkOnResume
	s.mu.Unlock()
	joined := strings.Join(args, " ")
	if resumeFrom != "source-session" || !forkOnResume {
		t.Fatalf("fork state = resume %q, fork %v", resumeFrom, forkOnResume)
	}
	if mcpCount != 1 || !strings.Contains(joined, "https://example.test/mcp") {
		t.Fatalf("fork lost MCP configuration: %s", joined)
	}
	if !strings.Contains(joined, additional) {
		t.Fatalf("fork lost additional directory: %s", joined)
	}
}
