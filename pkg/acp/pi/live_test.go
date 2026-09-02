package pi

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

type liveClient struct {
	mu         sync.Mutex
	text       strings.Builder
	toolTitles []string
	diffPaths  []string
}

func (c *liveClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errors.ErrUnsupported
}
func (c *liveClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, nil
}
func (c *liveClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errors.ErrUnsupported
}
func (c *liveClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errors.ErrUnsupported
}
func (c *liveClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errors.ErrUnsupported
}
func (c *liveClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errors.ErrUnsupported
}
func (c *liveClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errors.ErrUnsupported
}

func (c *liveClient) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if len(p.Options) == 0 {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Selected: &acp.RequestPermissionOutcomeSelected{OptionId: p.Options[0].OptionId},
	}}, nil
}

func (c *liveClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if u := n.Update.AgentMessageChunk; u != nil && u.Content.Text != nil {
		c.text.WriteString(u.Content.Text.Text)
	}
	if u := n.Update.ToolCall; u != nil {
		c.toolTitles = append(c.toolTitles, u.Title)
		c.recordDiffs(u.Content)
	}
	if u := n.Update.ToolCallUpdate; u != nil {
		if u.Title != nil {
			c.toolTitles = append(c.toolTitles, *u.Title)
		}
		c.recordDiffs(u.Content)
	}
	return nil
}

func (c *liveClient) recordDiffs(content []acp.ToolCallContent) {
	for _, item := range content {
		if item.Diff != nil {
			c.diffPaths = append(c.diffPaths, item.Diff.Path)
		}
	}
}

func (c *liveClient) snapshot() (string, []string, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text.String(), append([]string(nil), c.toolTitles...), append([]string(nil), c.diffPaths...)
}

func TestLivePiSession(t *testing.T) {
	if os.Getenv("PI_ACP_LIVE") == "" {
		t.Skip("set PI_ACP_LIVE=1 to run the live pi integration test")
	}
	path, err := exec.LookPath("pi")
	if err != nil {
		t.Skipf("pi not found: %v", err)
	}

	cwd, err := os.MkdirTemp("", "pi-live")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cwd)

	agentSide, clientSide := net.Pipe()
	agent := New(Options{Path: path, Env: os.Environ()})
	defer agent.Close()
	conn := acp.NewAgentSideConnection(agent, agentSide, agentSide)
	agent.SetAgentConnection(conn)

	client := &liveClient{}
	cc := acp.NewClientSideConnection(client, clientSide, clientSide)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	if _, err := cc.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ns, err := cc.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	r, err := cc.Prompt(ctx, acp.PromptRequest{
		SessionId: ns.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("Reply with exactly: OK")},
	})
	if err != nil {
		t.Fatalf("prompt 1: %v", err)
	}
	if r.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop = %q, want end_turn", r.StopReason)
	}
	text, _, _ := client.snapshot()
	if !strings.Contains(strings.ToUpper(text), "OK") {
		t.Errorf("reply = %q, want OK", text)
	}

	if _, err := cc.Prompt(ctx, acp.PromptRequest{
		SessionId: ns.SessionId,
		Prompt: []acp.ContentBlock{acp.TextBlock(
			"Use the write tool to create a file named diff-e2e.txt containing the word HELLO. Then run `cat diff-e2e.txt` with the bash tool. Then reply DONE.")},
	}); err != nil {
		t.Fatalf("prompt 2: %v", err)
	}

	text, titles, diffs := client.snapshot()
	t.Logf("tool titles: %q", titles)
	t.Logf("diff paths: %q", diffs)
	if !strings.Contains(strings.ToUpper(text), "DONE") {
		t.Errorf("reply = %q, want DONE", text)
	}
	if _, err := os.Stat(cwd + "/diff-e2e.txt"); err != nil {
		t.Errorf("diff-e2e.txt not created: %v", err)
	}

	sawDiff := false
	for _, p := range diffs {
		if strings.Contains(p, "diff-e2e.txt") {
			sawDiff = true
		}
	}
	if !sawDiff {
		t.Errorf("write tool produced no structured diff content; diffs = %q", diffs)
	}

	sawBashTitle := false
	for _, title := range titles {
		if strings.Contains(title, "cat diff-e2e.txt") {
			sawBashTitle = true
		}
	}
	if !sawBashTitle {
		t.Errorf("bash tool call not titled with command; titles = %q", titles)
	}
}
