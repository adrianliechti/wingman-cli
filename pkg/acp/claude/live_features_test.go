package claude

import (
	"context"
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
	stubClient
	pick func(acp.RequestPermissionRequest) acp.PermissionOptionId

	mu        sync.Mutex
	text      strings.Builder
	permCalls int
	modeIDs   []string
}

func (c *liveClient) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.permCalls++
	c.mu.Unlock()
	if len(p.Options) == 0 {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
	}
	id := p.Options[0].OptionId
	if c.pick != nil {
		id = c.pick(p)
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Selected: &acp.RequestPermissionOutcomeSelected{OptionId: id},
	}}, nil
}

func (c *liveClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if u := n.Update.AgentMessageChunk; u != nil && u.Content.Text != nil {
		c.text.WriteString(u.Content.Text.Text)
	}
	if u := n.Update.CurrentModeUpdate; u != nil {
		c.modeIDs = append(c.modeIDs, string(u.CurrentModeId))
	}
	return nil
}

func (c *liveClient) snapshot() (string, int, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text.String(), c.permCalls, append([]string(nil), c.modeIDs...)
}

func startLiveConn(t *testing.T, client acp.Client, caps ...acp.ClientCapabilities) (*acp.ClientSideConnection, context.Context, string) {
	t.Helper()
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
	cc := acp.NewClientSideConnection(client, clientSide, clientSide)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	init := acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}
	if len(caps) > 0 {
		init.ClientCapabilities = caps[0]
	}
	if _, err := cc.Initialize(ctx, init); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	cwd, err := os.MkdirTemp("", "claude-live")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(cwd) })
	return cc, ctx, cwd
}

func TestLiveAllowAlwaysPersists(t *testing.T) {
	client := &liveClient{pick: func(p acp.RequestPermissionRequest) acp.PermissionOptionId {
		for _, o := range p.Options {
			if o.Kind == acp.PermissionOptionKindAllowAlways {
				return o.OptionId
			}
		}
		return p.Options[0].OptionId
	}}
	cc, ctx, cwd := startLiveConn(t, client)

	ns, err := cc.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := cc.Prompt(ctx, acp.PromptRequest{
		SessionId: ns.SessionId,
		Prompt: []acp.ContentBlock{acp.TextBlock(
			"Run the shell command `touch e2e-marker.txt` with the Bash tool. When it finishes, run the exact same command `touch e2e-marker.txt` again as a second, separate Bash tool call. Two separate Bash invocations, same command. Then reply DONE.")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	text, perms, _ := client.snapshot()
	if !strings.Contains(strings.ToUpper(text), "DONE") {
		t.Errorf("reply = %q, want DONE", text)
	}
	if perms != 1 {
		t.Errorf("permission prompts = %d, want 1 (allow-always should persist for the repeated command)", perms)
	}
}

func TestLiveExitPlanModeSwitchesMode(t *testing.T) {
	client := &liveClient{}
	cc, ctx, cwd := startLiveConn(t, client)

	ns, err := cc.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := cc.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: ns.SessionId, ModeId: "plan"}); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	if _, err := cc.Prompt(ctx, acp.PromptRequest{
		SessionId: ns.SessionId,
		Prompt: []acp.ContentBlock{acp.TextBlock(
			"Make a one-sentence plan for creating a file hello.txt, then call ExitPlanMode to exit plan mode.")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	_, perms, modes := client.snapshot()
	if perms == 0 {
		t.Fatalf("expected an ExitPlanMode permission request")
	}
	found := false
	for _, m := range modes {
		if m == "agent" {
			found = true
		}
	}
	if !found {
		t.Errorf("current_mode_update notifications = %v, want one with \"agent\"", modes)
	}
}

func TestLiveSubagentProseIsolated(t *testing.T) {
	client := &liveClient{}
	cc, ctx, cwd := startLiveConn(t, client)

	ns, err := cc.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := cc.Prompt(ctx, acp.PromptRequest{
		SessionId: ns.SessionId,
		Prompt: []acp.ContentBlock{acp.TextBlock(
			"Launch a Task subagent (general-purpose) with this prompt: \"State the word WATERMELON and write three sentences about why watermelons are heavy.\" When the subagent finishes, do not repeat or summarize anything it said. Reply with exactly: FINISHED")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	text, _, _ := client.snapshot()
	if got := strings.Count(text, "FINISHED"); got != 1 {
		t.Errorf("reply = %q, want FINISHED exactly once (0 = lost, 2 = double-emitted)", text)
	}
	if strings.Contains(strings.ToUpper(text), "WATERMELON") {
		t.Errorf("subagent prose leaked into the top-level message feed: %q", text)
	}
}

func TestLiveAskUserQuestion(t *testing.T) {
	client := &liveClient{pick: func(p acp.RequestPermissionRequest) acp.PermissionOptionId {
		for _, o := range p.Options {
			if o.Name == "Blue" {
				return o.OptionId
			}
		}
		return p.Options[0].OptionId
	}}
	cc, ctx, cwd := startLiveConn(t, client)

	ns, err := cc.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := cc.Prompt(ctx, acp.PromptRequest{
		SessionId: ns.SessionId,
		Prompt: []acp.ContentBlock{acp.TextBlock(
			"Use the AskUserQuestion tool to ask me one question: \"Which color?\" with the options Red and Blue. After I answer, reply with exactly the color I chose and nothing else.")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	text, perms, _ := client.snapshot()
	if perms == 0 {
		t.Fatal("AskUserQuestion produced no permission request")
	}
	if !strings.Contains(text, "Blue") || strings.Contains(text, "Red") {
		t.Errorf("reply = %q, want the selected answer Blue", text)
	}
}

type formClient struct {
	liveClient
	mu       sync.Mutex
	requests int
}

func (c *formClient) UnstableCreateElicitation(_ context.Context, req acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
	if req.Form == nil {
		return acp.UnstableCreateElicitationResponse{Cancel: &acp.UnstableCreateElicitationCancel{Action: "cancel"}}, nil
	}
	return acp.UnstableCreateElicitationResponse{Accept: &acp.UnstableCreateElicitationAccept{
		Action:  "accept",
		Content: map[string]any{"question_0": "Blue"},
	}}, nil
}

func (c *formClient) UnstableCompleteElicitation(context.Context, acp.UnstableCompleteElicitationNotification) error {
	return nil
}

func TestLiveAskUserQuestionForm(t *testing.T) {
	client := &formClient{}
	cc, ctx, cwd := startLiveConn(t, client, acp.ClientCapabilities{
		Elicitation: &acp.ElicitationCapabilities{Form: &acp.ElicitationFormCapabilities{}},
	})

	ns, err := cc.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := cc.Prompt(ctx, acp.PromptRequest{
		SessionId: ns.SessionId,
		Prompt: []acp.ContentBlock{acp.TextBlock(
			"Use the AskUserQuestion tool to ask me one question: \"Which color?\" with the options Red and Blue. After I answer, reply with exactly the color I chose and nothing else.")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	text, perms, _ := client.snapshot()
	client.mu.Lock()
	forms := client.requests
	client.mu.Unlock()
	if forms == 0 {
		t.Fatal("AskUserQuestion did not use form elicitation despite advertised capability")
	}
	if perms != 0 {
		t.Errorf("expected no permission-based fallback, got %d permission requests", perms)
	}
	if !strings.Contains(text, "Blue") || strings.Contains(text, "Red") {
		t.Errorf("reply = %q, want the form answer Blue", text)
	}
}

func TestLiveSlashCommandOutput(t *testing.T) {
	client := &liveClient{}
	cc, ctx, cwd := startLiveConn(t, client)

	ns, err := cc.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := cc.Prompt(ctx, acp.PromptRequest{
		SessionId: ns.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("/model")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	text, _, _ := client.snapshot()
	t.Logf("slash command output: %q", text)
	if strings.TrimSpace(text) == "" {
		t.Error("slash command produced no visible output")
	}
}
