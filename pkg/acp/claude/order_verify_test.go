package claude

import (
	"context"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

type orderClient struct {
	stubClient
	mu          sync.Mutex
	seenToolIDs map[acp.ToolCallId]bool
	violations  []string
	permCalls   int
}

func (c *orderClient) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.permCalls++
	if !c.seenToolIDs[p.ToolCall.ToolCallId] {
		c.violations = append(c.violations, "permission request referenced unseen tool_call id "+string(p.ToolCall.ToolCallId))
	}
	c.mu.Unlock()
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Selected: &acp.RequestPermissionOutcomeSelected{OptionId: p.Options[0].OptionId},
	}}, nil
}

func (c *orderClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if u := n.Update.ToolCall; u != nil {
		c.seenToolIDs[u.ToolCallId] = true
	}
	if u := n.Update.ToolCallUpdate; u != nil && !c.seenToolIDs[u.ToolCallId] {
		c.violations = append(c.violations, "tool_call_update for unseen id "+string(u.ToolCallId))
	}
	return nil
}

// TestLiveToolCallOrdering guards against a permission request or
// tool_call_update referencing a tool_call id the client hasn't been told
// about yet (see toolCallTracker): it drives several Write-tool turns, which
// reliably trigger a permission request, against the real claude CLI and
// asserts every tool_call_update and permission request referenced an id
// that arrived via a prior tool_call.
func TestLiveToolCallOrdering(t *testing.T) {
	if os.Getenv("CLAUDE_ACP_LIVE") == "" {
		t.Skip("set CLAUDE_ACP_LIVE=1 to run the live claude integration test")
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		t.Skipf("claude not found: %v", err)
	}

	agentSide, clientSide := net.Pipe()
	agent := New(Options{Env: os.Environ(), Path: path})
	conn := acp.NewAgentSideConnection(agent, agentSide, agentSide)
	agent.SetAgentConnection(conn)

	client := &orderClient{seenToolIDs: map[acp.ToolCallId]bool{}}
	cc := acp.NewClientSideConnection(client, clientSide, clientSide)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if _, err := cc.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	cwd, _ := os.MkdirTemp("", "claude-live-order")
	defer os.RemoveAll(cwd)
	ns, err := cc.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	for i := range 3 {
		name := string(rune('a' + i))
		if _, err := cc.Prompt(ctx, acp.PromptRequest{
			SessionId: ns.SessionId,
			Prompt: []acp.ContentBlock{acp.TextBlock(
				"Create a file named " + name + ".txt containing the word marker using the Write tool, then stop.")},
		}); err != nil {
			t.Fatalf("prompt %d: %v", i, err)
		}
	}

	client.mu.Lock()
	violations := append([]string(nil), client.violations...)
	seen := len(client.seenToolIDs)
	perms := client.permCalls
	client.mu.Unlock()

	if perms == 0 {
		t.Fatal("expected at least one permission request for the Write tool")
	}
	if len(violations) > 0 {
		t.Fatalf("tool_call ordering violations: %v", violations)
	}
	if seen == 0 {
		t.Fatal("expected at least one tool_call to have been observed")
	}

	if _, err := cc.CloseSession(ctx, acp.CloseSessionRequest{SessionId: ns.SessionId}); err != nil {
		t.Fatalf("close session: %v", err)
	}
}
