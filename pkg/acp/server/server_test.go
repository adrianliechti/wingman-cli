package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/session"
)

type recordingClient struct {
	mu            sync.Mutex
	toolStarts    int
	toolCompletes int
	startIDs      []acpsdk.ToolCallId
	completeIDs   []acpsdk.ToolCallId
	permissionIDs []acpsdk.ToolCallId
	updates       chan struct{}
}

func (c *recordingClient) RequestPermission(_ context.Context, p acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.permissionIDs = append(c.permissionIDs, p.ToolCall.ToolCallId)
	c.mu.Unlock()
	if len(p.Options) == 0 {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{},
		}}, nil
	}
	return acpsdk.RequestPermissionResponse{Outcome: acpsdk.RequestPermissionOutcome{
		Selected: &acpsdk.RequestPermissionOutcomeSelected{OptionId: p.Options[0].OptionId},
	}}, nil
}

func (c *recordingClient) SessionUpdate(_ context.Context, n acpsdk.SessionNotification) error {
	c.mu.Lock()
	if n.Update.ToolCall != nil {
		c.toolStarts++
		c.startIDs = append(c.startIDs, n.Update.ToolCall.ToolCallId)
	}
	if u := n.Update.ToolCallUpdate; u != nil && u.Status != nil && *u.Status == acpsdk.ToolCallStatusCompleted {
		c.toolCompletes++
		c.completeIDs = append(c.completeIDs, u.ToolCallId)
	}
	c.mu.Unlock()
	if c.updates != nil {
		c.updates <- struct{}{}
	}
	return nil
}

func TestACPConfirmAnnouncesPermissionLifecycle(t *testing.T) {
	agentSide, clientSide := net.Pipe()
	defer agentSide.Close()
	defer clientSide.Close()

	s := &Server{}
	s.conn = acpsdk.NewAgentSideConnection(s, agentSide, agentSide)
	client := &recordingClient{updates: make(chan struct{}, 2)}
	_ = acpsdk.NewClientSideConnection(client, clientSide, clientSide)

	ctx, cancel := context.WithTimeout(code.WithSessionID(context.Background(), "session-1"), 2*time.Second)
	defer cancel()
	allowed, err := s.Confirm(ctx, "Run command?")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("permission was not allowed")
	}
	for range 2 {
		select {
		case <-client.updates:
		case <-ctx.Done():
			t.Fatal("timed out waiting for permission lifecycle updates")
		}
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.startIDs) != 1 || len(client.permissionIDs) != 1 || len(client.completeIDs) != 1 {
		t.Fatalf("permission lifecycle = starts:%v requests:%v completes:%v", client.startIDs, client.permissionIDs, client.completeIDs)
	}
	if client.startIDs[0] != client.permissionIDs[0] || client.startIDs[0] != client.completeIDs[0] {
		t.Fatalf("permission lifecycle IDs differ: starts:%v requests:%v completes:%v", client.startIDs, client.permissionIDs, client.completeIDs)
	}
}

func TestACPAdvertisedElicitationFailureCancelsInsteadOfAcceptingDefaults(t *testing.T) {
	agentSide, clientSide := net.Pipe()
	defer agentSide.Close()
	defer clientSide.Close()

	s := &Server{}
	s.conn = acpsdk.NewAgentSideConnection(s, agentSide, agentSide)
	s.formElicitation.Store(true)
	_ = acpsdk.NewClientSideConnection(&recordingClient{}, clientSide, clientSide)

	ctx, cancel := context.WithTimeout(code.WithSessionID(context.Background(), "session-1"), 2*time.Second)
	defer cancel()
	result, err := s.Elicit(ctx, tool.ElicitRequest{Fields: []tool.ElicitField{{
		Name:    "answer",
		Default: "fabricated",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != tool.ElicitCancel {
		t.Fatalf("elicitation action = %q, want cancel", result.Action)
	}
}

func (*recordingClient) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, errors.ErrUnsupported
}

func (*recordingClient) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, errors.ErrUnsupported
}

func (*recordingClient) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, errors.ErrUnsupported
}

func (*recordingClient) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, errors.ErrUnsupported
}

func (*recordingClient) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, errors.ErrUnsupported
}

func (*recordingClient) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, errors.ErrUnsupported
}

func (*recordingClient) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, errors.ErrUnsupported
}

func TestACPDeleteListedSessionWithoutLoading(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	dir := code.SessionsDir(cwd)
	const id = "listed-session"
	if err := session.Save(dir, id, agent.State{Messages: []agent.Message{{
		Role: agent.RoleUser,
		Content: []agent.Content{{
			Text: "hello",
		}},
	}}}); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		sessions:   map[acpsdk.SessionId]*sessionEntry{},
		workspaces: map[string]*workspaceEntry{},
	}
	init, err := s.Initialize(context.Background(), acpsdk.InitializeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if init.AgentCapabilities.SessionCapabilities.Delete == nil {
		t.Fatal("session/delete is not advertised")
	}
	listed, err := s.ListSessions(context.Background(), acpsdk.ListSessionsRequest{Cwd: &cwd})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].SessionId != id {
		t.Fatalf("listed sessions = %+v, want %q", listed.Sessions, id)
	}

	params := acpsdk.UnstableDeleteSessionRequest{SessionId: id}
	if _, err := s.UnstableDeleteSession(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Load(dir, id); err == nil {
		t.Fatal("deleted session is still loadable")
	}
	if _, err := s.UnstableDeleteSession(context.Background(), params); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

func TestACPTaskTurnWritesFileAndReportsUsage(t *testing.T) {
	home := t.TempDir()
	workdir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	unsetEnv(t, "WINGMAN_URL")
	unsetEnv(t, "WINGMAN_TOKEN")
	unsetEnv(t, "WINGMAN_MODEL")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_DEFAULT_MODEL", "gpt-5.4")

	var responseCalls atomic.Int64
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.4","object":"model","created":0,"owned_by":"test"}]}`))
		case "/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			call := responseCalls.Add(1)
			if call == 1 {
				writeSSE(t, w, map[string]any{
					"type":            "response.output_item.done",
					"sequence_number": 1,
					"output_index":    0,
					"item": map[string]any{
						"type":      "function_call",
						"id":        "fc_1",
						"call_id":   "call_1",
						"name":      "write",
						"arguments": fmt.Sprintf(`{"file_path":%q,"content":"benchmark-ready\n"}`, filepath.Join(workdir, "solution.txt")),
						"status":    "completed",
					},
				})
				writeSSE(t, w, completedEvent(2, 11, 2, 3, nil))
				return
			}
			writeSSE(t, w, completedEvent(1, 13, 4, 5, []map[string]any{{
				"type":   "message",
				"id":     "msg_1",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{{
					"type":        "output_text",
					"text":        "done",
					"annotations": []any{},
				}},
			}}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer modelServer.Close()
	t.Setenv("OPENAI_BASE_URL", modelServer.URL)

	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		config:     cfg,
		sessions:   map[acpsdk.SessionId]*sessionEntry{},
		workspaces: map[string]*workspaceEntry{},
	}

	agentSide, clientSide := net.Pipe()
	defer agentSide.Close()
	defer clientSide.Close()
	s.conn = acpsdk.NewAgentSideConnection(s, agentSide, agentSide)
	client := &recordingClient{}
	conn := acpsdk.NewClientSideConnection(client, clientSide, clientSide)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := conn.Initialize(ctx, acpsdk.InitializeRequest{ProtocolVersion: acpsdk.ProtocolVersionNumber}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	session, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: workdir, McpServers: []acpsdk.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	response, err := conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: session.SessionId,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("Create solution.txt for the benchmark.")},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if response.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want end_turn", response.StopReason)
	}
	if response.Usage == nil {
		t.Fatal("prompt response did not include usage")
	}
	if response.Usage.InputTokens != 18 || response.Usage.OutputTokens != 8 || response.Usage.TotalTokens != 32 {
		t.Fatalf("usage = %+v, want input=18 cached=6 output=8 total=32", *response.Usage)
	}
	if response.Usage.CachedReadTokens == nil || *response.Usage.CachedReadTokens != 6 {
		t.Fatalf("cached usage = %v, want 6", response.Usage.CachedReadTokens)
	}
	content, err := os.ReadFile(filepath.Join(workdir, "solution.txt"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(content) != "benchmark-ready\n" {
		t.Fatalf("result content = %q", content)
	}
	client.mu.Lock()
	starts, completes := client.toolStarts, client.toolCompletes
	client.mu.Unlock()
	if starts != 1 || completes != 1 {
		t.Fatalf("tool events = starts:%d completes:%d, want 1 each", starts, completes)
	}
	if _, err := conn.CloseSession(ctx, acpsdk.CloseSessionRequest{SessionId: session.SessionId}); err != nil {
		t.Fatalf("close session: %v", err)
	}
	if got := responseCalls.Load(); got != 2 {
		t.Fatalf("model response calls = %d, want 2", got)
	}
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	value, present := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

func writeSSE(t *testing.T, w http.ResponseWriter, event map[string]any) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}

func completedEvent(sequence, input, cached, output int, items []map[string]any) map[string]any {
	response := map[string]any{
		"usage": map[string]any{
			"input_tokens": input,
			"input_tokens_details": map[string]any{
				"cached_tokens": cached,
			},
			"output_tokens": output,
		},
	}
	if items != nil {
		response["output"] = items
	}
	return map[string]any{
		"type":            "response.completed",
		"sequence_number": sequence,
		"response":        response,
	}
}

func TestTokenCountSaturates(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if got := tokenCount(-1); got != 0 {
		t.Fatalf("negative count = %d, want 0", got)
	}
	if got := addTokenCounts(maxInt, 1); got != maxInt {
		t.Fatalf("saturated sum = %d, want %d", got, maxInt)
	}
	if got := tokenCount(42); got != 42 {
		t.Fatalf("ordinary token count = %d, want 42", got)
	}
}
