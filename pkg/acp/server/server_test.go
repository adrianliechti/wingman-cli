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

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
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

func TestNotifyContentPreservesToolFailureStatus(t *testing.T) {
	var update acpsdk.SessionUpdate
	notifyContent(func(got acpsdk.SessionUpdate) { update = got }, agent.RoleAssistant, agent.Content{
		ToolResult: &agent.ToolResult{ID: "call-1", Name: "shell", Content: "failed", IsError: true},
	})

	if update.ToolCallUpdate == nil || update.ToolCallUpdate.Status == nil {
		t.Fatalf("tool update = %+v", update.ToolCallUpdate)
	}
	if got := *update.ToolCallUpdate.Status; got != acpsdk.ToolCallStatusFailed {
		t.Fatalf("tool status = %q, want %q", got, acpsdk.ToolCallStatusFailed)
	}
}

func TestNotifyContentIgnoresPartialToolCall(t *testing.T) {
	called := false
	notifyContent(func(acpsdk.SessionUpdate) { called = true }, agent.RoleAssistant, agent.Content{
		ToolCall: &agent.ToolCall{ID: "call-1", Name: "shell", Partial: true},
	})
	if called {
		t.Fatal("partial tool call was sent to ACP")
	}
}

func TestNotifyContentPreservesMixedTextAndImage(t *testing.T) {
	var updates []acpsdk.SessionUpdate
	notifyContent(func(update acpsdk.SessionUpdate) { updates = append(updates, update) }, agent.RoleUser, agent.Content{
		Text: "describe this",
		File: &agent.File{Data: "data:image/png;base64,AA=="},
	})
	if len(updates) != 2 || updates[0].UserMessageChunk == nil || updates[0].UserMessageChunk.Content.Text == nil ||
		updates[0].UserMessageChunk.Content.Text.Text != "describe this" || updates[1].UserMessageChunk == nil ||
		updates[1].UserMessageChunk.Content.Image == nil || updates[1].UserMessageChunk.Content.Image.Data != "AA==" {
		t.Fatalf("updates = %#v", updates)
	}
}

func TestNotifyContentUsesCompactFilePresentation(t *testing.T) {
	var update acpsdk.SessionUpdate
	notifyContent(func(got acpsdk.SessionUpdate) { update = got }, agent.RoleAssistant, agent.Content{
		ToolCall: &agent.ToolCall{
			ID: "read-1", Name: "read",
			Args: `{"file_path":"/workspace/main.go","offset":12,"limit":4}`,
		},
	})

	call := update.ToolCall
	if call == nil || call.Title != "Read file" || call.Kind != acpsdk.ToolKindRead {
		t.Fatalf("tool call = %#v", call)
	}
	if len(call.Locations) != 1 || call.Locations[0].Path != "/workspace/main.go" ||
		call.Locations[0].Line == nil || *call.Locations[0].Line != 12 {
		t.Fatalf("locations = %#v", call.Locations)
	}
	args, ok := call.RawInput.(map[string]any)
	if !ok || args["limit"] != float64(4) || args["offset"] != nil || args["file_path"] != nil || args["path"] != nil {
		t.Fatalf("display input = %#v", call.RawInput)
	}
}

func TestNotifyContentDoesNotRepeatEditPayload(t *testing.T) {
	var update acpsdk.SessionUpdate
	notifyContent(func(got acpsdk.SessionUpdate) { update = got }, agent.RoleAssistant, agent.Content{
		ToolCall: &agent.ToolCall{
			ID: "edit-1", Name: "edit",
			Args: `{"file_path":"/workspace/main.go","old_string":"before","new_string":"after"}`,
		},
	})

	call := update.ToolCall
	if call == nil || call.Title != "Edit file" || len(call.Locations) != 1 {
		t.Fatalf("tool call = %#v", call)
	}
	if call.RawInput != nil {
		t.Fatalf("edit payload duplicated its diff/result: %#v", call.RawInput)
	}
}

func TestClassifyPromptStreamError(t *testing.T) {
	reason, err := classifyPromptStreamError(context.Canceled)
	if err != nil || reason != acpsdk.StopReasonCancelled {
		t.Fatalf("cancelled stream = %q, %v", reason, err)
	}

	want := errors.New("model stream failed")
	reason, err = classifyPromptStreamError(want)
	if !errors.Is(err, want) || reason != "" {
		t.Fatalf("failed stream = %q, %v", reason, err)
	}
}

func TestRetainSessionQueuesPromptsAndHonorsWaitingContext(t *testing.T) {
	const id = acpsdk.SessionId("session-1")
	w := &workspaceEntry{refs: 1}
	s := &Server{sessions: map[acpsdk.SessionId]*sessionEntry{
		id: {id: id, workspace: w},
	}}

	_, finishFirst, err := s.retainSession(context.Background(), id, func() {})
	if err != nil {
		t.Fatal(err)
	}
	type retained struct {
		finish func()
		err    error
	}
	second := make(chan retained, 1)
	go func() {
		_, finish, err := s.retainSession(context.Background(), id, func() {})
		second <- retained{finish: finish, err: err}
	}()
	select {
	case got := <-second:
		t.Fatalf("second prompt was not queued: %v", got.err)
	case <-time.After(20 * time.Millisecond):
	}

	waitCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if _, _, err := s.retainSession(waitCtx, id, func() {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting prompt error = %v, want context.Canceled", err)
	}

	finishFirst()
	var got retained
	select {
	case got = <-second:
	case <-time.After(time.Second):
		t.Fatal("queued prompt did not start after the prior prompt finished")
	}
	if got.err != nil || got.finish == nil {
		t.Fatalf("queued prompt = %#v", got)
	}
	got.finish()
	// Prompt retains are normally paired with the Prompt method's deferred
	// workspace releases. Keep that invariant in this isolated lifecycle test.
	s.releaseWorkspace(w)
	s.releaseWorkspace(w)
}

func TestReplaceLoadedSessionSwapsWorkspaceAndRejectsBusySession(t *testing.T) {
	const id = acpsdk.SessionId("session-1")
	oldAgent := &codeagent.Agent{}
	newAgent := &codeagent.Agent{}
	oldWorkspace := &workspaceEntry{agent: oldAgent, key: "old", refs: 2}
	newWorkspace := &workspaceEntry{agent: newAgent, key: "new", refs: 1}
	oldSession := &sessionEntry{id: id, agent: oldAgent, workspace: oldWorkspace}
	s := &Server{
		sessions:    map[acpsdk.SessionId]*sessionEntry{id: oldSession},
		sessionDirs: map[acpsdk.SessionId]string{id: "old-dir"},
		workspaces: map[string]*workspaceEntry{
			"old": oldWorkspace,
			"new": newWorkspace,
		},
	}

	if err := s.replaceLoadedSession(id, newWorkspace); err != nil {
		t.Fatal(err)
	}
	if got := s.lookupSession(id); got == oldSession || got.workspace != newWorkspace || got.agent != newAgent {
		t.Fatalf("replacement session = %#v", got)
	}
	if oldWorkspace.refs != 1 || newWorkspace.refs != 1 {
		t.Fatalf("workspace refs = old:%d new:%d", oldWorkspace.refs, newWorkspace.refs)
	}

	busy := s.lookupSession(id)
	busy.cancel = func() {}
	thirdWorkspace := &workspaceEntry{agent: &codeagent.Agent{}, key: "third", refs: 1}
	if err := s.replaceLoadedSession(id, thirdWorkspace); err == nil {
		t.Fatal("busy session was replaced")
	}
	if s.lookupSession(id) != busy || thirdWorkspace.refs != 1 {
		t.Fatal("failed replacement changed session ownership")
	}
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
	testenv.WingmanHome(t)
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
	testenv.UserHome(t)
	testenv.WingmanHome(t)
	workdir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
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
				writeSSE(t, w, completedEvent(2, 11, 2, 1, 3, 1, nil))
				return
			}
			writeSSE(t, w, completedEvent(1, 13, 4, 2, 5, 2, []map[string]any{{
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
	if response.Usage.InputTokens != 15 || response.Usage.OutputTokens != 5 || response.Usage.TotalTokens != 32 {
		t.Fatalf("usage = %+v, want input=15 output=5 total=32", *response.Usage)
	}
	if response.Usage.CachedReadTokens == nil || *response.Usage.CachedReadTokens != 6 {
		t.Fatalf("cache-read usage = %v, want 6", response.Usage.CachedReadTokens)
	}
	if response.Usage.CachedWriteTokens == nil || *response.Usage.CachedWriteTokens != 3 {
		t.Fatalf("cache-write usage = %v, want 3", response.Usage.CachedWriteTokens)
	}
	if response.Usage.ThoughtTokens == nil || *response.Usage.ThoughtTokens != 3 {
		t.Fatalf("reasoning usage = %v, want 3", response.Usage.ThoughtTokens)
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

func completedEvent(sequence, input, cached, cacheWrite, output, reasoning int, items []map[string]any) map[string]any {
	response := map[string]any{
		"usage": map[string]any{
			"input_tokens": input,
			"input_tokens_details": map[string]any{
				"cached_tokens":      cached,
				"cache_write_tokens": cacheWrite,
			},
			"output_tokens": output,
			"output_tokens_details": map[string]any{
				"reasoning_tokens": reasoning,
			},
			"total_tokens": input + output,
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
