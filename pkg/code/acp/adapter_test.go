package acp

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

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

func TestInitializationRejectsUnsupportedProtocolVersion(t *testing.T) {
	_, err := NewInProcess(context.Background(), &code.Workspace{RootPath: t.TempDir()}, "future", &adapterProtocolAgent{protocol: 2}, nil, nil)
	if err == nil {
		t.Fatal("unsupported protocol version was accepted")
	}
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
