package acp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func TestTextFileCallbacksSupportAbsolutePaths(t *testing.T) {
	base := t.TempDir()
	workspaceDir := filepath.Join(base, "workspace")
	outsideDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &Agent{workspace: &code.Workspace{RootPath: workspaceDir}}
	nested := filepath.Join(outsideDir, "nested", "file.txt")
	if _, err := a.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: nested, Content: "inside"}); err != nil {
		t.Fatalf("write absolute path outside cwd: %v", err)
	}
	resp, err := a.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: nested})
	if err != nil || resp.Content != "inside" {
		t.Fatalf("read absolute path outside cwd = %q, %v", resp.Content, err)
	}
	if _, err := a.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: "relative.txt"}); err == nil {
		t.Fatal("relative path was accepted")
	}
}

func TestToolCallContentTextRendersDiff(t *testing.T) {
	old := "line one\nold line\nline three\n"
	items := []acp.ToolCallContent{
		{Diff: &acp.ToolCallContentDiff{
			Type:    "diff",
			Path:    "/p/a.go",
			OldText: &old,
			NewText: "line one\nnew line\nline three\n",
		}},
	}
	got := toolCallContentText(items)
	if !strings.Contains(got, "/p/a.go") {
		t.Errorf("expected path in output:\n%s", got)
	}
	if !strings.Contains(got, "-old line") || !strings.Contains(got, "+new line") {
		t.Errorf("expected -/+ diff lines:\n%s", got)
	}
	if !strings.Contains(got, " line one") {
		t.Errorf("expected unchanged context line:\n%s", got)
	}
}

func TestToolCallContentTextAddedFile(t *testing.T) {

	items := []acp.ToolCallContent{
		{Diff: &acp.ToolCallContentDiff{
			Type:    "diff",
			Path:    "/p/new.go",
			NewText: "package main\n\nfunc main() {}\n",
		}},
	}
	got := toolCallContentText(items)
	if strings.Contains(got, "-") {
		t.Errorf("added file should have no removed lines:\n%s", got)
	}
	if !strings.Contains(got, "+package main") || !strings.Contains(got, "+func main() {}") {
		t.Errorf("expected all lines added:\n%s", got)
	}
}

func TestToolCallContentTextPlainText(t *testing.T) {
	items := []acp.ToolCallContent{
		{Content: &acp.ToolCallContentContent{Content: acp.TextBlock("hello output")}},
	}
	if got := toolCallContentText(items); got != "hello output" {
		t.Errorf("plain text = %q", got)
	}
}

func TestHistoryProvidersReturnVersionedClone(t *testing.T) {
	const sessionID = "session"
	a := &Agent{sessions: map[string]*sessionState{
		sessionID: {
			messages: []agent.Message{{
				Role:    agent.RoleAssistant,
				Content: []agent.Content{{Text: "original"}},
			}},
		},
	}}

	version := a.HistoryVersion(sessionID)
	if version.Revision != 0 || version.MessageCount != 1 {
		t.Fatalf("history version = %#v, want revision 0 and message count 1", version)
	}

	snapshot := a.HistorySnapshot(sessionID)
	if snapshot.Revision != 0 || len(snapshot.Messages) != 1 {
		t.Fatalf("history snapshot = %#v, want revision 0 and one message", snapshot)
	}
	snapshot.Messages[0].Content[0].Text = "mutated clone"
	if got := a.Messages(sessionID)[0].Content[0].Text; got != "original" {
		t.Fatalf("snapshot mutation changed retained history to %q", got)
	}
}

func modeState(modes ...string) *acp.SessionModeState {
	avail := make([]acp.SessionMode, 0, len(modes))
	for _, m := range modes {
		avail = append(avail, acp.SessionMode{Id: acp.SessionModeId(m), Name: m})
	}
	return &acp.SessionModeState{
		AvailableModes: avail,
		CurrentModeId:  acp.SessionModeId(modes[0]),
	}
}

func TestModesPerSession(t *testing.T) {
	a := &Agent{sessions: map[string]*sessionState{}}
	add := func(id string, modes ...string) {
		s := &sessionState{id: acp.SessionId(id)}
		s.applyModes(modeState(modes...))
		a.sessions[id] = s
	}
	add("a", "plan", "code")
	add("b", "code", "plan")

	if modes, cur := a.Modes("a"); cur != "plan" || len(modes) != 2 {
		t.Fatalf("session a = (%v, %q), want provider modes, current plan", modes, cur)
	}
	if _, cur := a.Modes("b"); cur != "code" {
		t.Fatalf("session b current = %q, want code", cur)
	}
	if modes, cur := a.Modes("missing"); modes != nil || cur != "" {
		t.Fatalf("unknown session = (%v, %q), want (nil, \"\")", modes, cur)
	}
}

func TestTranslateUpdateSuppressesPromptUserEcho(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{}
	turn := &turn{ignoreUserUpdates: true}

	if msg, ok := a.translateUpdate(sess, turn, acp.UpdateUserMessageText("echo")); ok {
		t.Fatalf("prompt user echo was emitted: %+v", msg)
	}
	if len(turn.emitted) != 0 {
		t.Fatalf("prompt user echo was persisted: %+v", turn.emitted)
	}

	turn.ignoreUserUpdates = false
	if _, ok := a.translateUpdate(sess, turn, acp.UpdateUserMessageText("history")); !ok {
		t.Fatal("load-session user message was suppressed")
	}
}

func TestTranslateUpdateCoalescesACPTextChunksForTranscript(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{}
	turn := &turn{}

	for _, chunk := range []string{"Hi", "!", " What", " are", " we", " working", " on", " today", "?"} {
		if msg, ok := a.translateUpdate(sess, turn, acp.UpdateAgentMessageText(chunk)); !ok || msg.Content[0].Text != chunk {
			t.Fatalf("live chunk %q = %+v, ok=%v", chunk, msg, ok)
		}
	}
	if len(turn.emitted) != 1 || len(turn.emitted[0].Content) != 1 {
		t.Fatalf("persisted messages = %+v", turn.emitted)
	}
	if got := turn.emitted[0].Content[0].Text; got != "Hi! What are we working on today?" {
		t.Fatalf("persisted text = %q", got)
	}
}

func TestTranslateUpdateKeepsDistinctACPMessageIDsSeparate(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{}
	turn := &turn{}

	for _, item := range []struct{ id, text string }{{"message-1", "First"}, {"message-2", "Second"}} {
		update := acp.UpdateAgentMessageText(item.text)
		update.AgentMessageChunk.MessageId = &item.id
		a.translateUpdate(sess, turn, update)
	}
	if len(turn.emitted) != 1 || len(turn.emitted[0].Content) != 2 {
		t.Fatalf("distinct ACP messages were merged: %+v", turn.emitted)
	}
	for i, want := range []string{"message-1", "message-2"} {
		if got := turn.emitted[0].Content[i].TextID; got != want {
			t.Fatalf("content %d text ID = %q, want %q", i, got, want)
		}
	}
}

func TestTranslateUpdateCoalescesReasoningChunksByID(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{}
	turn := &turn{}
	id := "reasoning-1"
	for _, chunk := range []string{"first", " second"} {
		update := acp.UpdateAgentThoughtText(chunk)
		update.AgentThoughtChunk.MessageId = &id
		if _, ok := a.translateUpdate(sess, turn, update); !ok {
			t.Fatalf("reasoning chunk %q was dropped", chunk)
		}
	}
	if len(turn.emitted) != 1 || len(turn.emitted[0].Content) != 1 || turn.emitted[0].Content[0].Reasoning.Summary != "first second" {
		t.Fatalf("persisted reasoning = %+v", turn.emitted)
	}
}

func TestTranslateUpdateKeepsToolBoundaryBetweenTextRuns(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{toolCalls: map[string]toolCall{}}
	turn := &turn{}

	for _, chunk := range []string{"Let me", " check."} {
		a.translateUpdate(sess, turn, acp.UpdateAgentMessageText(chunk))
	}
	id := acp.ToolCallId("call-1")
	a.translateUpdate(sess, turn, acp.StartToolCall(id, "shell"))
	a.translateUpdate(sess, turn, acp.UpdateToolCall(
		id,
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock("done"))}),
	))
	for _, chunk := range []string{"It", " works."} {
		a.translateUpdate(sess, turn, acp.UpdateAgentMessageText(chunk))
	}

	if len(turn.emitted) != 1 || len(turn.emitted[0].Content) != 4 {
		t.Fatalf("persisted content = %+v", turn.emitted)
	}
	content := turn.emitted[0].Content
	if content[0].Text != "Let me check." || content[1].ToolCall == nil || content[2].ToolResult == nil || content[3].Text != "It works." {
		t.Fatalf("tool boundary was not preserved: %+v", content)
	}
}

func TestTranslateUpdateReleasesCompletedToolCall(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{toolCalls: map[string]toolCall{}}
	turn := &turn{}
	id := acp.ToolCallId("call-1")

	if _, ok := a.translateUpdate(sess, turn, acp.StartToolCall(id, "shell")); !ok {
		t.Fatal("tool call start was not translated")
	}
	if len(sess.toolCalls) != 1 {
		t.Fatalf("in-flight tool calls = %d, want 1", len(sess.toolCalls))
	}

	update := acp.UpdateToolCall(
		id,
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateContent([]acp.ToolCallContent{
			acp.ToolContent(acp.TextBlock("done")),
		}),
	)
	if _, ok := a.translateUpdate(sess, turn, update); !ok {
		t.Fatal("tool call completion was not translated")
	}
	if len(sess.toolCalls) != 0 {
		t.Fatalf("completed tool call was retained: %+v", sess.toolCalls)
	}
}

func TestTranslateUpdatePreservesCommandActionTitle(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{toolCalls: map[string]toolCall{}}
	turn := &turn{}
	id := acp.ToolCallId("command-1")
	update := acp.StartToolCall(
		id,
		"Run command",
		acp.WithStartKind(acp.ToolKindExecute),
		acp.WithStartRawInput(map[string]any{"command": "sed -n '1,20p' README.md", "cwd": "/workspace"}),
	)
	msg, ok := a.translateUpdate(sess, turn, update)
	if !ok || len(msg.Content) != 1 || msg.Content[0].ToolCall == nil {
		t.Fatalf("translated command = %+v, ok=%v", msg, ok)
	}
	call := msg.Content[0].ToolCall
	if call.Name != "Run command" || call.Kind != "execute" || !strings.Contains(call.Args, `"command":"sed -n '1,20p' README.md"`) {
		t.Fatalf("command = %+v", call)
	}
}

func TestTranslateUpdatePreservesSemanticDiffKind(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{toolCalls: map[string]toolCall{}}
	turn := &turn{}
	id := acp.ToolCallId("edit-1")
	old := "old\n"

	start, ok := a.translateUpdate(sess, turn, acp.StartToolCall(
		id,
		"Editing files",
		acp.WithStartKind(acp.ToolKindEdit),
	))
	if !ok || start.Content[0].ToolCall == nil || start.Content[0].ToolCall.Kind != "edit" {
		t.Fatalf("translated start = %+v, ok=%v", start, ok)
	}

	result, ok := a.translateUpdate(sess, turn, acp.UpdateToolCall(
		id,
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateContent([]acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{
			Type: "diff", Path: "/workspace/a.go", OldText: &old, NewText: "new\n",
		}}}),
	))
	if !ok || result.Content[0].ToolResult == nil || result.Content[0].ToolResult.Kind != "edit" {
		t.Fatalf("translated result = %+v, ok=%v", result, ok)
	}
}

func TestTranslateUpdatePreservesToolLocations(t *testing.T) {
	workspaceRoot := t.TempDir()
	a := &Agent{workspace: &code.Workspace{RootPath: workspaceRoot}}
	sess := &sessionState{toolCalls: map[string]toolCall{}}
	turn := &turn{}
	id := acp.ToolCallId("read-1")
	line := 12
	path := filepath.Join(workspaceRoot, "main.go")

	start, ok := a.translateUpdate(sess, turn, acp.StartToolCall(
		id,
		"Read main.go",
		acp.WithStartKind(acp.ToolKindRead),
		acp.WithStartLocations([]acp.ToolCallLocation{{Path: path, Line: &line}}),
	))
	if !ok || start.Content[0].ToolCall == nil {
		t.Fatalf("translated start = %+v, ok=%v", start, ok)
	}
	locations := start.Content[0].ToolCall.Locations
	if len(locations) != 1 || locations[0].Path != "main.go" || locations[0].Line != 12 {
		t.Fatalf("start locations = %+v", locations)
	}

	updatedLine := 24
	result, ok := a.translateUpdate(sess, turn, acp.UpdateToolCall(
		id,
		acp.WithUpdateLocations([]acp.ToolCallLocation{{Path: path, Line: &updatedLine}}),
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock("contents"))}),
	))
	if !ok || result.Content[0].ToolResult == nil {
		t.Fatalf("translated result = %+v, ok=%v", result, ok)
	}
	locations = result.Content[0].ToolResult.Locations
	if len(locations) != 1 || locations[0].Line != 24 {
		t.Fatalf("result locations = %+v", locations)
	}
}

func TestTranslateUpdateInfersEditKindFromStructuredDiff(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{toolCalls: map[string]toolCall{}}
	turn := &turn{}
	id := acp.ToolCallId("edit-1")

	if _, ok := a.translateUpdate(sess, turn, acp.StartToolCall(id, "Changed a file")); !ok {
		t.Fatal("tool call start was not translated")
	}
	result, ok := a.translateUpdate(sess, turn, acp.UpdateToolCall(
		id,
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateContent([]acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{
			Type: "diff", Path: "/workspace/new.go", NewText: "package main\n",
		}}}),
	))
	if !ok || result.Content[0].ToolResult == nil || result.Content[0].ToolResult.Kind != "edit" {
		t.Fatalf("translated result = %+v, ok=%v", result, ok)
	}
}

func TestTranslateUpdateRetainsStartDiffForStatusOnlyCompletion(t *testing.T) {
	a := &Agent{}
	sess := &sessionState{toolCalls: map[string]toolCall{}}
	turn := &turn{}
	id := acp.ToolCallId("edit-1")
	old := "before\n"

	if _, ok := a.translateUpdate(sess, turn, acp.StartToolCall(
		id,
		"Edit file",
		acp.WithStartKind(acp.ToolKindEdit),
		acp.WithStartContent([]acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{
			Type: "diff", Path: "main.go", OldText: &old, NewText: "after\n",
		}}}),
	)); !ok {
		t.Fatal("tool call start was not translated")
	}

	result, ok := a.translateUpdate(sess, turn, acp.UpdateToolCall(
		id,
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
	))
	if !ok || len(result.Content) != 1 || result.Content[0].ToolResult == nil {
		t.Fatalf("translated result = %+v, ok=%v", result, ok)
	}
	if body := result.Content[0].ToolResult.Content; !strings.Contains(body, "-before") || !strings.Contains(body, "+after") {
		t.Fatalf("retained diff = %q", body)
	}
}

func steerTestAgent(fn func(context.Context, acp.SessionId, []acp.ContentBlock, string) error) (*Agent, *sessionState, *turn) {
	t := &turn{}
	sess := &sessionState{id: "session-1", inflight: t}
	a := &Agent{
		steer: fn,
		sessions: map[string]*sessionState{
			"session-1": sess,
		},
	}
	return a, sess, t
}

func TestSteerAcceptedBeforeFinalizationIsPersistedWithTurn(t *testing.T) {
	a, sess, active := steerTestAgent(func(context.Context, acp.SessionId, []acp.ContentBlock, string) error {
		return nil
	})
	input := code.TurnInput{ID: "input-1", Content: []agent.Content{{Text: "guide"}}}
	if err := a.Steer(context.Background(), "session-1", input); err != nil {
		t.Fatal(err)
	}
	sess.finalizeTurn(active)

	if len(sess.messages) != 1 || sess.messages[0].Role != agent.RoleUser || sess.messages[0].Content[0].Text != "guide" {
		t.Fatalf("messages = %+v", sess.messages)
	}
}

func TestSteerAcceptedAfterFinalizationIsStillPersisted(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	a, sess, active := steerTestAgent(func(context.Context, acp.SessionId, []acp.ContentBlock, string) error {
		close(started)
		<-release
		return nil
	})
	done := make(chan error, 1)
	go func() {
		done <- a.Steer(context.Background(), "session-1", code.TurnInput{
			ID: "input-1", Content: []agent.Content{{Text: "guide"}},
		})
	}()
	<-started
	sess.finalizeTurn(active)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if len(sess.messages) != 1 || sess.messages[0].Role != agent.RoleUser || sess.messages[0].Content[0].Text != "guide" {
		t.Fatalf("messages = %+v", sess.messages)
	}
}

func TestSteerFailureDoesNotPersistInput(t *testing.T) {
	want := errors.New("steer failed")
	a, sess, active := steerTestAgent(func(context.Context, acp.SessionId, []acp.ContentBlock, string) error {
		return want
	})
	err := a.Steer(context.Background(), "session-1", code.TurnInput{
		ID: "input-1", Content: []agent.Content{{Text: "guide"}},
	})
	if !errors.Is(err, want) {
		t.Fatalf("steer error = %v", err)
	}
	sess.finalizeTurn(active)
	if len(sess.messages) != 0 {
		t.Fatalf("failed steer was persisted: %+v", sess.messages)
	}
}

func TestSteerRequiresInflightTurn(t *testing.T) {
	a, sess, _ := steerTestAgent(func(context.Context, acp.SessionId, []acp.ContentBlock, string) error {
		return nil
	})
	sess.inflight = nil
	err := a.Steer(context.Background(), "session-1", code.TurnInput{ID: "input-1", Content: []agent.Content{{Text: "guide"}}})
	if !errors.Is(err, code.ErrNoActiveTurn) {
		t.Fatalf("steer error = %v", err)
	}
}

type fakeUI struct {
	elicit        func(tool.ElicitRequest) (tool.ElicitResult, error)
	elicitContext func(context.Context, tool.ElicitRequest) (tool.ElicitResult, error)
	confirm       func(string) (bool, error)
}

func (u *fakeUI) Elicit(ctx context.Context, req tool.ElicitRequest) (tool.ElicitResult, error) {
	if u.elicitContext != nil {
		return u.elicitContext(ctx, req)
	}
	if u.elicit == nil {
		return tool.ElicitResult{}, errors.New("elicit unsupported")
	}
	return u.elicit(req)
}

func (u *fakeUI) Confirm(_ context.Context, message string) (bool, error) {
	if u.confirm == nil {
		return false, errors.New("confirm unsupported")
	}
	return u.confirm(message)
}

func TestElicitFieldsFromSchema(t *testing.T) {
	schema := acp.UnstableElicitationSchema{
		Type: acp.UnstableElicitationSchemaTypeObject,
		Properties: map[string]any{
			"question_0": map[string]any{
				"type":  "string",
				"title": "Color",
				"oneOf": []any{
					map[string]any{"const": "Red", "title": "Red", "description": "warm"},
					map[string]any{"const": "Blue", "title": "Blue"},
				},
			},
			"question_0_custom": map[string]any{
				"type": "string", "title": "Other",
				"_meta": map[string]any{"_askUserQuestionCustomAnswer": map[string]any{
					"questionId": "question_0", "isCustomAnswer": true,
				}},
			},
			"question_1": map[string]any{
				"type":        "array",
				"description": "Pick letters",
				"items":       map[string]any{"anyOf": []any{map[string]any{"const": "a"}, map[string]any{"const": "b"}}},
			},
		},
		Required: []string{"question_0"},
	}

	fields := elicitFieldsFromSchema(schema)
	if len(fields) != 3 {
		t.Fatalf("fields = %#v", fields)
	}
	if fields[0].Name != "question_0" || !fields[0].Required || !fields[0].Strict ||
		len(fields[0].Enum) != 2 || fields[0].Enum[0] != "Red" || fields[0].EnumDescriptions[0] != "warm" {
		t.Errorf("enum field = %#v", fields[0])
	}
	if fields[1].Name != "question_0_custom" || fields[1].Strict || len(fields[1].Enum) != 0 || fields[1].CustomAnswerFor != "question_0" {
		t.Errorf("text field = %#v", fields[1])
	}
	if fields[2].Name != "question_1" || !fields[2].Multiple || len(fields[2].Enum) != 2 || fields[2].EnumDescriptions != nil {
		t.Errorf("array field = %#v", fields[2])
	}
}

func TestUnstableCreateElicitationFlow(t *testing.T) {
	form := &acp.UnstableCreateElicitationForm{
		Mode:    "form",
		Message: "Which color?",
		RequestedSchema: acp.UnstableElicitationSchema{
			Type:       acp.UnstableElicitationSchemaTypeObject,
			Properties: map[string]any{"question_0": map[string]any{"type": "string"}},
		},
	}

	a := &Agent{}
	resp, err := a.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{Form: form})
	if err != nil || resp.Decline == nil {
		t.Fatalf("headless should decline, got %#v err=%v", resp, err)
	}

	a.SetUI(&fakeUI{elicit: func(req tool.ElicitRequest) (tool.ElicitResult, error) {
		if req.Message != "Which color?" || len(req.Fields) != 1 {
			t.Errorf("request = %#v", req)
		}
		return tool.ElicitResult{Action: tool.ElicitAccept, Content: map[string]any{"question_0": "Blue"}}, nil
	}})
	resp, err = a.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{Form: form})
	if err != nil || resp.Accept == nil || resp.Accept.Content["question_0"] != "Blue" {
		t.Fatalf("accept = %#v err=%v", resp, err)
	}

	a.SetUI(&fakeUI{elicit: func(tool.ElicitRequest) (tool.ElicitResult, error) {
		return tool.ElicitResult{Action: tool.ElicitDecline}, nil
	}})
	if resp, _ = a.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{Form: form}); resp.Decline == nil {
		t.Fatalf("decline = %#v", resp)
	}

	a.SetUI(&fakeUI{})
	if resp, _ = a.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{Form: form}); resp.Cancel == nil {
		t.Fatalf("elicit error should cancel, got %#v", resp)
	}
}

func TestFormElicitationUsesMetadataSessionWithConcurrentTurns(t *testing.T) {
	a := &Agent{sessions: map[string]*sessionState{
		"session-1": {id: "session-1", inflight: &turn{}},
		"session-2": {id: "session-2", inflight: &turn{}},
	}}
	seen := ""
	a.SetUI(&fakeUI{elicitContext: func(ctx context.Context, _ tool.ElicitRequest) (tool.ElicitResult, error) {
		seen = code.SessionIDFromContext(ctx)
		return tool.ElicitResult{Action: tool.ElicitDecline}, nil
	}})

	form := &acp.UnstableCreateElicitationForm{
		Meta:    map[string]any{"sessionId": "session-2"},
		Mode:    "form",
		Message: "Choose",
		RequestedSchema: acp.UnstableElicitationSchema{
			Type:       acp.UnstableElicitationSchemaTypeObject,
			Properties: map[string]any{},
		},
	}
	if _, err := a.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{Form: form}); err != nil {
		t.Fatal(err)
	}
	if seen != "session-2" {
		t.Fatalf("elicitation session = %q, want session-2", seen)
	}
}

func TestRequestPermissionChoice(t *testing.T) {
	req := acp.RequestPermissionRequest{
		SessionId: "s1",
		Options: []acp.PermissionOption{
			{OptionId: "ask-0", Name: "Red", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "ask-1", Name: "Blue", Kind: acp.PermissionOptionKindAllowOnce, Meta: map[string]any{
				"permission": map[string]any{"version": 1, "changes": []any{
					map[string]any{"description": "Allow blue for this session"},
				}},
			}},
			{OptionId: "ask-skip", Name: "Skip", Kind: acp.PermissionOptionKindRejectOnce},
		},
	}

	a := &Agent{}
	a.SetUI(&fakeUI{elicit: func(req tool.ElicitRequest) (tool.ElicitResult, error) {
		if len(req.Fields) != 1 || !req.Fields[0].Strict || len(req.Fields[0].Enum) != 3 {
			t.Errorf("choice field = %#v", req.Fields)
		}
		if got := req.Fields[0].EnumDescriptions; len(got) != 3 || got[1] != "Allow blue for this session" {
			t.Errorf("choice descriptions = %#v", got)
		}
		return tool.ElicitResult{Action: tool.ElicitAccept, Content: map[string]any{"choice": "Blue"}}, nil
	}})
	resp, err := a.RequestPermission(context.Background(), req)
	if err != nil || resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "ask-1" {
		t.Fatalf("choice outcome = %#v err=%v", resp, err)
	}

	a.SetUI(&fakeUI{elicit: func(tool.ElicitRequest) (tool.ElicitResult, error) {
		return tool.ElicitResult{Action: tool.ElicitDecline}, nil
	}})
	resp, _ = a.RequestPermission(context.Background(), req)
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "ask-skip" {
		t.Fatalf("decline should pick reject option, got %#v", resp)
	}

	a.SetUI(&fakeUI{confirm: func(string) (bool, error) { return true, nil }})
	resp, _ = a.RequestPermission(context.Background(), req)
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "ask-0" {
		t.Fatalf("confirm fallback should pick first allow, got %#v", resp)
	}

	a = &Agent{}
	resp, _ = a.RequestPermission(context.Background(), req)
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "ask-skip" {
		t.Fatalf("headless should choose the explicit reject option, got %#v", resp)
	}
}

func TestRequestPermissionHeadlessCancelsWithoutRejectOption(t *testing.T) {
	a := &Agent{}
	resp, err := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce}},
	})
	if err != nil || resp.Outcome.Cancelled == nil {
		t.Fatalf("headless permission response = %#v, err=%v", resp, err)
	}
}

func TestUnattendedSelectsAllowOnceWithoutUI(t *testing.T) {
	a := &Agent{sessions: map[string]*sessionState{
		"s1": {modeID: code.UnattendedModeID},
	}}
	resp, err := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		SessionId: "s1",
		Options: []acp.PermissionOption{
			{OptionId: "once", Name: "Allow Once", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "always", Name: "Always Allow", Kind: acp.PermissionOptionKindAllowAlways},
			{OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil || resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "once" {
		t.Fatalf("unattended permission = %#v, err=%v", resp, err)
	}
}

func TestUnattendedResolvesFormWithoutUI(t *testing.T) {
	a := &Agent{sessions: map[string]*sessionState{
		"s1": {id: "s1", modeID: code.UnattendedModeID},
	}}
	resp, err := a.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{Form: &acp.UnstableCreateElicitationForm{
		Meta:    map[string]any{"sessionId": "s1"},
		Mode:    "form",
		Message: "Choose a color",
		RequestedSchema: acp.UnstableElicitationSchema{
			Type: acp.UnstableElicitationSchemaTypeObject,
			Properties: map[string]any{
				"color": map[string]any{"type": "string", "enum": []any{"Blue", "Red"}},
			},
			Required: []string{"color"},
		},
	}})
	if err != nil || resp.Accept == nil || resp.Accept.Content["color"] != "Blue" {
		t.Fatalf("unattended elicitation = %#v, err=%v", resp, err)
	}
}
