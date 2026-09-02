package acp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

type adapterProtocolAgent struct {
	acpsdk.Agent

	protocol acpsdk.ProtocolVersion
	caps     acpsdk.AgentCapabilities

	mu             sync.Mutex
	listCursors    []string
	resumeCalls    int
	newCalls       int
	configRequests []acpsdk.SetSessionConfigOptionRequest
	options        []acpsdk.SessionConfigOption
	promptFn       func(context.Context, acpsdk.PromptRequest) (acpsdk.PromptResponse, error)
	cancelCalls    int
	loadFn         func(context.Context, acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error)
	loadCalls      int
}

func (a *adapterProtocolAgent) Initialize(context.Context, acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	version := a.protocol
	if version == 0 {
		version = acpsdk.ProtocolVersionNumber
	}
	return acpsdk.InitializeResponse{ProtocolVersion: version, AgentCapabilities: a.caps}, nil
}

func (a *adapterProtocolAgent) ListSessions(_ context.Context, req acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	cursor := ""
	if req.Cursor != nil {
		cursor = *req.Cursor
	}
	a.mu.Lock()
	a.listCursors = append(a.listCursors, cursor)
	a.mu.Unlock()
	if cursor == "" {
		next := "page-2"
		return acpsdk.ListSessionsResponse{
			Sessions:   []acpsdk.SessionInfo{{SessionId: "one", Cwd: *req.Cwd}},
			NextCursor: &next,
		}, nil
	}
	return acpsdk.ListSessionsResponse{Sessions: []acpsdk.SessionInfo{{SessionId: "two", Cwd: *req.Cwd}}}, nil
}

func (a *adapterProtocolAgent) ResumeSession(context.Context, acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	a.mu.Lock()
	a.resumeCalls++
	a.mu.Unlock()
	return acpsdk.ResumeSessionResponse{}, nil
}

func (a *adapterProtocolAgent) NewSession(context.Context, acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.newCalls++
	return acpsdk.NewSessionResponse{SessionId: acpsdk.SessionId(fmt.Sprintf("new-%d", a.newCalls)), ConfigOptions: a.options}, nil
}

func (a *adapterProtocolAgent) SetSessionConfigOption(_ context.Context, req acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	if req.ValueId == nil {
		return acpsdk.SetSessionConfigOptionResponse{}, fmt.Errorf("expected select value")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.configRequests = append(a.configRequests, req)
	options := cloneOptionsWithCurrent(a.options, req.ValueId.ConfigId, req.ValueId.Value)
	a.options = options
	return acpsdk.SetSessionConfigOptionResponse{ConfigOptions: options}, nil
}

func (a *adapterProtocolAgent) Prompt(ctx context.Context, req acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	if a.promptFn != nil {
		return a.promptFn(ctx, req)
	}
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

func (a *adapterProtocolAgent) Cancel(context.Context, acpsdk.CancelNotification) error {
	a.mu.Lock()
	a.cancelCalls++
	a.mu.Unlock()
	return nil
}

func (a *adapterProtocolAgent) LoadSession(ctx context.Context, req acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	a.mu.Lock()
	a.loadCalls++
	a.mu.Unlock()
	if a.loadFn != nil {
		return a.loadFn(ctx, req)
	}
	return acpsdk.LoadSessionResponse{}, nil
}

func TestPreSessionUpdateIsAppliedAfterNewSessionResponse(t *testing.T) {
	remote := &adapterProtocolAgent{}
	a := newAdapterForTest(t, remote)
	a.SessionUpdate(context.Background(), acpsdk.SessionNotification{SessionId: "new-1", Update: acpsdk.SessionUpdate{
		AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{AvailableCommands: []acpsdk.AvailableCommand{{Name: "review"}}},
	}})

	id, err := a.NewSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	commands := a.Commands(id)
	if len(commands) != 1 || commands[0].Name != "review" {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestConfigOptionIDsRemainSessionScoped(t *testing.T) {
	modelCategory := acpsdk.SessionConfigOptionCategoryModel
	values := acpsdk.SessionConfigSelectOptionsUngrouped{{Name: "Small", Value: "small"}, {Name: "Large", Value: "large"}}
	option := func(id string) []acpsdk.SessionConfigOption {
		return []acpsdk.SessionConfigOption{{Select: &acpsdk.SessionConfigOptionSelect{
			Id: acpsdk.SessionConfigId(id), Name: "Model", Type: "select", Category: &modelCategory, CurrentValue: "small",
			Options: acpsdk.SessionConfigSelectOptions{Ungrouped: &values},
		}}}
	}
	remote := &adapterProtocolAgent{options: option("first-model")}
	a := newAdapterForTest(t, remote)
	first, err := a.NewSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.options = option("second-model")
	remote.mu.Unlock()
	if _, err := a.NewSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.SetModel(context.Background(), first, "large"); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	requests := append([]acpsdk.SetSessionConfigOptionRequest(nil), remote.configRequests...)
	remote.mu.Unlock()
	if len(requests) != 1 || string(requests[0].ValueId.ConfigId) != "first-model" {
		t.Fatalf("config requests = %#v", requests)
	}
}

func newAdapterForTest(t *testing.T, remote acpsdk.Agent) *Agent {
	t.Helper()
	a, err := NewInProcess(context.Background(), &code.Workspace{RootPath: t.TempDir()}, "test", remote, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestListSessionsFollowsPagination(t *testing.T) {
	remote := &adapterProtocolAgent{caps: acpsdk.AgentCapabilities{SessionCapabilities: acpsdk.SessionCapabilities{
		List: &acpsdk.SessionListCapabilities{},
	}}}
	a := newAdapterForTest(t, remote)
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != "one" || sessions[1].ID != "two" {
		t.Fatalf("sessions = %#v", sessions)
	}
	remote.mu.Lock()
	cursors := append([]string(nil), remote.listCursors...)
	remote.mu.Unlock()
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "page-2" {
		t.Fatalf("cursors = %#v", cursors)
	}
}

func TestLoadSessionFallsBackToResumeCapability(t *testing.T) {
	remote := &adapterProtocolAgent{caps: acpsdk.AgentCapabilities{SessionCapabilities: acpsdk.SessionCapabilities{
		Resume: &acpsdk.SessionResumeCapabilities{},
	}}}
	a := newAdapterForTest(t, remote)
	if err := a.LoadSession(context.Background(), "existing"); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	calls := remote.resumeCalls
	remote.mu.Unlock()
	if calls != 1 {
		t.Fatalf("resume calls = %d", calls)
	}
}

func TestLoadSessionStreamReplaysAndCachesHistory(t *testing.T) {
	var serverConn *acpsdk.AgentSideConnection
	remote := &adapterProtocolAgent{caps: acpsdk.AgentCapabilities{LoadSession: true}}
	remote.loadFn = func(ctx context.Context, req acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
		updates := []acpsdk.SessionUpdate{
			acpsdk.UpdateUserMessageText("question"),
			acpsdk.UpdateUserMessage(acpsdk.ImageBlock("AA==", "image/png")),
			acpsdk.UpdateAgentMessageText("ans"),
			acpsdk.UpdateAgentMessageText("wer"),
			acpsdk.StartToolCall("call-1", "Read file", acpsdk.WithStartKind(acpsdk.ToolKindRead)),
			acpsdk.UpdateToolCall("call-1",
				acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
				acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("contents"))}),
			),
		}
		for _, update := range updates {
			if err := serverConn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: req.SessionId, Update: update}); err != nil {
				return acpsdk.LoadSessionResponse{}, err
			}
		}
		return acpsdk.LoadSessionResponse{}, nil
	}
	a, err := NewInProcess(
		context.Background(), &code.Workspace{RootPath: t.TempDir()}, "test", remote,
		func(conn *acpsdk.AgentSideConnection) { serverConn = conn }, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var final []agent.Message
	for snapshot, err := range a.LoadSessionStream(context.Background(), "existing") {
		if err != nil {
			t.Fatal(err)
		}
		final = snapshot
	}
	if len(final) != 2 || final[0].Role != agent.RoleUser || len(final[0].Content) != 2 ||
		final[0].Content[0].Text != "question" || final[0].Content[1].File == nil ||
		final[0].Content[1].File.Data != "data:image/png;base64,AA==" {
		t.Fatalf("history = %#v", final)
	}
	if len(final[1].Content) != 3 || final[1].Content[0].Text != "answer" || final[1].Content[1].ToolCall == nil || final[1].Content[2].ToolResult == nil {
		t.Fatalf("assistant history = %#v", final[1])
	}
	for _, err := range a.LoadSessionStream(context.Background(), "existing") {
		if err != nil {
			t.Fatal(err)
		}
	}
	remote.mu.Lock()
	loadCalls := remote.loadCalls
	remote.mu.Unlock()
	if loadCalls != 1 {
		t.Fatalf("remote load calls = %d, want cached replay after the first", loadCalls)
	}
}

func TestInitializationRejectsUnsupportedProtocolVersion(t *testing.T) {
	_, err := NewInProcess(context.Background(), &code.Workspace{RootPath: t.TempDir()}, "future", &adapterProtocolAgent{protocol: 2}, nil, nil)
	if err == nil {
		t.Fatal("unsupported protocol version was accepted")
	}
}

func TestNewSessionRejectsEmptyRemoteSessionID(t *testing.T) {
	bad := &emptySessionIDAgent{adapterProtocolAgent: &adapterProtocolAgent{}}
	a := newAdapterForTest(t, bad)
	if _, err := a.NewSession(context.Background()); err == nil {
		t.Fatal("empty remote session ID was accepted")
	}
}

type emptySessionIDAgent struct{ *adapterProtocolAgent }

func (*emptySessionIDAgent) NewSession(context.Context, acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	return acpsdk.NewSessionResponse{}, nil
}

func TestGroupedCategoryConfigAndNewSessionDefaults(t *testing.T) {
	modelCategory := acpsdk.SessionConfigOptionCategoryModel
	effortCategory := acpsdk.SessionConfigOptionCategoryThoughtLevel
	models := acpsdk.SessionConfigSelectOptionsGrouped{{
		Group: "recommended", Name: "Recommended",
		Options: []acpsdk.SessionConfigSelectOption{{Name: "Small", Value: "small"}, {Name: "Large", Value: "large"}},
	}}
	efforts := acpsdk.SessionConfigSelectOptionsUngrouped{{Name: "Low", Value: "low"}, {Name: "High", Value: "high"}}
	remote := &adapterProtocolAgent{options: []acpsdk.SessionConfigOption{
		{Select: &acpsdk.SessionConfigOptionSelect{
			Id: "model-choice", Name: "Model", Type: "select", Category: &modelCategory, CurrentValue: "small",
			Options: acpsdk.SessionConfigSelectOptions{Grouped: &models},
		}},
		{Select: &acpsdk.SessionConfigOptionSelect{
			Id: "thinking", Name: "Thinking", Type: "select", Category: &effortCategory, CurrentValue: "low",
			Options: acpsdk.SessionConfigSelectOptions{Ungrouped: &efforts},
		}},
	}}
	a := newAdapterForTest(t, remote)
	if err := a.SetModel(context.Background(), "", "large"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEffort(context.Background(), "", "high"); err != nil {
		t.Fatal(err)
	}
	id, err := a.NewSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	available, current := a.Models(id)
	if len(available) != 2 || current != "large" {
		t.Fatalf("models = %#v current=%q", available, current)
	}
	if current, values := a.Effort(id); current != "high" || len(values) != 2 {
		t.Fatalf("effort = %q values=%#v", current, values)
	}
	remote.mu.Lock()
	requests := append([]acpsdk.SetSessionConfigOptionRequest(nil), remote.configRequests...)
	remote.mu.Unlock()
	if len(requests) != 2 || string(requests[0].ValueId.ConfigId) != "model-choice" || string(requests[1].ValueId.ConfigId) != "thinking" {
		t.Fatalf("config requests = %#v", requests)
	}
}

func TestModelDescriptionIsPreserved(t *testing.T) {
	modelCategory := acpsdk.SessionConfigOptionCategoryModel
	description := "Claude Sonnet 4.6"
	models := acpsdk.SessionConfigSelectOptionsUngrouped{{
		Name: "Default", Value: "default", Description: &description,
	}}
	remote := &adapterProtocolAgent{options: []acpsdk.SessionConfigOption{{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id: "model", Name: "Model", Type: "select", Category: &modelCategory, CurrentValue: "default",
			Options: acpsdk.SessionConfigSelectOptions{Ungrouped: &models},
		},
	}}}
	a := newAdapterForTest(t, remote)
	id, err := a.NewSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	available, current := a.Models(id)
	if current != "default" || len(available) != 1 || available[0].Description != description {
		t.Fatalf("models = %#v current=%q", available, current)
	}
}

func TestSessionUpdatesPreserveCommandsMetadataUsageAndProgress(t *testing.T) {
	progress := make(chan string, 1)
	ctx := tool.WithProgressSink(context.Background(), func(_ context.Context, id, text string) { progress <- id + ":" + text })
	turn := &turn{ctx: ctx, events: make(chan event, 1), done: make(chan struct{})}
	sess := &sessionState{id: "session", inflight: turn, toolCalls: map[string]toolCall{}}
	a := &Agent{sessions: map[string]*sessionState{"session": sess}}

	a.SessionUpdate(context.Background(), acpsdk.SessionNotification{SessionId: "session", Update: acpsdk.SessionUpdate{
		AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{AvailableCommands: []acpsdk.AvailableCommand{{
			Name: "review", Description: "Review changes", Input: &acpsdk.AvailableCommandInput{Unstructured: &acpsdk.UnstructuredCommandInput{Hint: "focus"}},
		}}},
	}})
	updated := time.Now().UTC().Truncate(time.Second)
	updatedText := updated.Format(time.RFC3339)
	title := "Updated title"
	a.SessionUpdate(context.Background(), acpsdk.SessionNotification{SessionId: "session", Update: acpsdk.SessionUpdate{
		SessionInfoUpdate: &acpsdk.SessionSessionInfoUpdate{Title: &title, UpdatedAt: &updatedText},
	}})
	a.SessionUpdate(context.Background(), acpsdk.SessionNotification{SessionId: "session", Update: acpsdk.SessionUpdate{
		UsageUpdate: &acpsdk.SessionUsageUpdate{Used: 123, Size: 456},
	}})
	toolID := acpsdk.ToolCallId("call")
	a.SessionUpdate(context.Background(), acpsdk.SessionNotification{SessionId: "session", Update: acpsdk.UpdateToolCall(
		toolID, acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusInProgress), acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("working"))}),
	)})

	commands := a.Commands("session")
	if len(commands) != 1 || commands[0].Name != "review" || commands[0].InputHint != "focus" {
		t.Fatalf("commands = %#v", commands)
	}
	sess.mu.Lock()
	gotTitle, gotUpdated, usage := sess.title, sess.updatedAt, sess.usage
	sess.mu.Unlock()
	if gotTitle != title || !gotUpdated.Equal(updated) {
		t.Fatalf("metadata = %q %v", gotTitle, gotUpdated)
	}
	if usage.InputTokens != 0 || usage.LastInputTokens != 123 || usage.ContextWindow != 456 {
		t.Fatalf("usage = %#v", usage)
	}
	select {
	case got := <-progress:
		if got != "call:working" {
			t.Fatalf("progress = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("tool progress was not reported")
	}
}

func TestSendPreservesMixedInputAndACPStreamState(t *testing.T) {
	var serverConn *acpsdk.AgentSideConnection
	requests := make(chan acpsdk.PromptRequest, 1)
	remote := &adapterProtocolAgent{}
	remote.promptFn = func(ctx context.Context, req acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		requests <- req
		for _, text := range []string{"hel", "lo"} {
			if err := serverConn.SessionUpdate(ctx, acpsdk.SessionNotification{
				SessionId: req.SessionId,
				Update:    acpsdk.UpdateAgentMessageText(text),
			}); err != nil {
				return acpsdk.PromptResponse{}, err
			}
		}
		if err := serverConn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: req.SessionId,
			Update:    acpsdk.UpdateAgentMessage(acpsdk.ImageBlock("AA==", "image/png")),
		}); err != nil {
			return acpsdk.PromptResponse{}, err
		}
		toolID := acpsdk.ToolCallId("call-1")
		if err := serverConn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: req.SessionId,
			Update:    acpsdk.StartToolCall(toolID, "Read file", acpsdk.WithStartKind(acpsdk.ToolKindRead)),
		}); err != nil {
			return acpsdk.PromptResponse{}, err
		}
		if err := serverConn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: req.SessionId,
			Update: acpsdk.UpdateToolCall(toolID,
				acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
				acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("contents"))}),
			),
		}); err != nil {
			return acpsdk.PromptResponse{}, err
		}
		cached := 2
		return acpsdk.PromptResponse{
			StopReason: acpsdk.StopReasonEndTurn,
			Usage: &acpsdk.Usage{
				InputTokens: 7, CachedReadTokens: &cached, OutputTokens: 3, TotalTokens: 12,
			},
		}, nil
	}

	a, err := NewInProcess(
		context.Background(), &code.Workspace{RootPath: t.TempDir()}, "test", remote,
		func(conn *acpsdk.AgentSideConnection) { serverConn = conn }, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	sessionID, err := a.NewSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	input := []agent.Content{{
		Text: "describe this",
		File: &agent.File{Name: "pixel.png", Data: "data:image/png;base64,AA=="},
	}}
	stream, err := a.Send(context.Background(), sessionID, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
	}

	req := <-requests
	if len(req.Prompt) != 2 || req.Prompt[0].Text == nil || req.Prompt[1].Image == nil {
		t.Fatalf("prompt blocks = %#v, want text followed by image", req.Prompt)
	}
	messages := a.Messages(sessionID)
	if len(messages) != 2 || len(messages[1].Content) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[1].Content[0].Text != "hello" || messages[1].Content[1].File == nil ||
		messages[1].Content[1].File.Data != "data:image/png;base64,AA==" ||
		messages[1].Content[2].ToolCall == nil || messages[1].Content[3].ToolResult == nil {
		t.Fatalf("assistant content = %#v", messages[1].Content)
	}
	if usage := a.Usage(sessionID); usage.InputTokens != 7 || usage.CachedTokens != 2 || usage.OutputTokens != 3 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestSendSurfacesCancelledStopReasonWithoutRedundantCancel(t *testing.T) {
	remote := &adapterProtocolAgent{}
	remote.promptFn = func(context.Context, acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	}
	a := newAdapterForTest(t, remote)
	sessionID, err := a.NewSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := a.Send(context.Background(), sessionID, []agent.Content{{Text: "cancelled remotely"}})
	if err != nil {
		t.Fatal(err)
	}
	var streamErr error
	for _, err := range stream {
		streamErr = err
	}
	if !errors.Is(streamErr, context.Canceled) {
		t.Fatalf("stream error = %v, want context.Canceled", streamErr)
	}
	remote.mu.Lock()
	cancelCalls := remote.cancelCalls
	remote.mu.Unlock()
	if cancelCalls != 0 {
		t.Fatalf("session/cancel calls = %d, want 0 after a terminal cancelled response", cancelCalls)
	}
}

func cloneOptionsWithCurrent(options []acpsdk.SessionConfigOption, id acpsdk.SessionConfigId, value acpsdk.SessionConfigValueId) []acpsdk.SessionConfigOption {
	result := append([]acpsdk.SessionConfigOption(nil), options...)
	for i, option := range result {
		if option.Select == nil || option.Select.Id != id {
			continue
		}
		copy := *option.Select
		copy.CurrentValue = value
		result[i].Select = &copy
	}
	return result
}
