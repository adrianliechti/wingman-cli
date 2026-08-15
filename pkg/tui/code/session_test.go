package code

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func newStreamTestApp(messages []agent.Message) (*App, *uiTestAgent) {
	testAgent := newUITestAgent(messages)
	return &App{
		ctx: context.Background(), agent: testAgent, sessionID: "session",
		term: inline.NewTerminal(), queue: make(chan func(), 64), quit: make(chan struct{}),
	}, testAgent
}

func TestSyncMessagesReconcilesNativeCommittedStream(t *testing.T) {
	a, testAgent := newStreamTestApp(nil)
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Reasoning: &agent.Reasoning{ID: "reason-1", Summary: "planning the edit"},
	}}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Text: "Let me inspect the file.",
	}}})

	testAgent.messages = append(testAgent.messages, agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{Reasoning: &agent.Reasoning{ID: "reason-1", Summary: "planning the edit"}},
		{Text: "Let me inspect the file."},
		{ToolCall: &agent.ToolCall{ID: "call-1", Name: "read", Args: `{"path":"main.go"}`}},
	}})
	a.syncMessages()

	if tail := ansi.Strip(strings.Join(a.streamCells(100), "\n")); strings.Contains(tail, "planning the edit") || strings.Contains(tail, "Let me inspect the file.") {
		t.Fatalf("committed response remained in live tail: %q", tail)
	}

	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolCall: &agent.ToolCall{ID: "call-1", Name: "read", Args: `{"path":"main.go"}`},
	}}})
	result := &agent.ToolResult{ID: "call-1", Name: "read", Args: `{"path":"main.go"}`, Content: "package main"}
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: result}}})
	testAgent.messages = append(testAgent.messages, agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: result}}})
	a.syncMessages()

	out := ansi.Strip(strings.Join(a.chatViewLines(100), "\n"))
	if count := strings.Count(out, "Let me inspect the file."); count != 1 {
		t.Fatalf("assistant response rendered %d times: %q", count, out)
	}
	if count := strings.Count(out, "planning the edit"); count != 1 {
		t.Fatalf("reasoning rendered %d times: %q", count, out)
	}
	if textAt, toolAt := strings.Index(out, "Let me inspect the file."), strings.Index(out, "main.go"); textAt < 0 || toolAt <= textAt {
		t.Fatalf("tool result is not after its response: %q", out)
	}
}

func TestSyncMessagesRebuildsRewrittenHistory(t *testing.T) {
	a, testAgent := newStreamTestApp([]agent.Message{{Role: agent.RoleUser, Content: []agent.Content{{Text: "old history"}}}})
	a.syncMessages()

	testAgent.messages = []agent.Message{{Role: agent.RoleUser, Content: []agent.Content{{Text: "compacted history"}}}}
	testAgent.revision++
	a.syncMessages()

	out := ansi.Strip(strings.Join(a.chatViewLines(100), "\n"))
	if strings.Contains(out, "old history") || strings.Count(out, "compacted history") != 1 {
		t.Fatalf("history rewrite was appended instead of rebuilt: %q", out)
	}
}

func TestSyncMessagesSkipsUnchangedHistoryClone(t *testing.T) {
	a, testAgent := newStreamTestApp([]agent.Message{{Role: agent.RoleUser, Content: []agent.Content{{Text: "hello"}}}})
	a.syncMessages()
	if testAgent.snapshots != 1 {
		t.Fatalf("initial history snapshots = %d, want 1", testAgent.snapshots)
	}

	a.syncMessages()
	if testAgent.snapshots != 1 {
		t.Fatalf("unchanged history was cloned again: snapshots = %d", testAgent.snapshots)
	}

	testAgent.messages = append(testAgent.messages, agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "answer"}}})
	a.syncMessages()
	if testAgent.snapshots != 2 {
		t.Fatalf("appended history was not cloned: snapshots = %d", testAgent.snapshots)
	}
}

func TestStreamResetDropsOnlyFailedAttemptOutput(t *testing.T) {
	a, _ := newStreamTestApp(nil)
	a.appendLiveUserEcho("keep this guidance")
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "failed partial"}}})
	a.handleTurnEvent(code.TurnEvent{SessionID: "session", StreamEvent: agent.StreamEventReset})

	tail := ansi.Strip(strings.Join(a.streamCells(100), "\n"))
	if strings.Contains(tail, "failed partial") || !strings.Contains(tail, "keep this guidance") {
		t.Fatalf("retry reset removed the wrong live content: %q", tail)
	}
}

func TestStreamResetPreservesCommittedEarlierRound(t *testing.T) {
	a, _ := newStreamTestApp(nil)
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Text: "accepted output", TextID: "message-1",
	}}})
	a.handleTurnEvent(code.TurnEvent{SessionID: "session", StreamEvent: agent.StreamEventCommit})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Text: "failed output", TextID: "message-2",
	}}})
	a.handleTurnEvent(code.TurnEvent{SessionID: "session", StreamEvent: agent.StreamEventReset})

	tail := ansi.Strip(strings.Join(a.streamCells(100), "\n"))
	if !strings.Contains(tail, "accepted output") || strings.Contains(tail, "failed output") {
		t.Fatalf("retry reset crossed the committed attempt boundary: %q", tail)
	}
}

func TestPartialToolCallUpdatesInPlaceAndResets(t *testing.T) {
	a, _ := newStreamTestApp(nil)
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolCall: &agent.ToolCall{ID: "call-1", Name: "shell", Partial: true},
	}}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolCall: &agent.ToolCall{ID: "call-1", Name: "shell", Args: `{"command":"go test"}`, Partial: true},
	}}})

	snapshots := a.snapshotStreamState()
	if len(snapshots) != 1 || snapshots[0].toolArgs != `{"command":"go test"}` || !snapshots[0].toolPartial {
		t.Fatalf("partial snapshots did not merge: %+v", snapshots)
	}

	a.handleTurnEvent(code.TurnEvent{SessionID: "session", StreamEvent: agent.StreamEventReset})
	if snapshots := a.snapshotStreamState(); len(snapshots) != 0 {
		t.Fatalf("failed partial tool survived reset: %+v", snapshots)
	}
}

func TestStreamCommitReplacesPartialToolWithDefinitiveCall(t *testing.T) {
	a, _ := newStreamTestApp(nil)
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolCall: &agent.ToolCall{ID: "call-1", Name: "shell", Args: `{"command":"go te"}`, Partial: true},
	}}})
	a.handleTurnEvent(code.TurnEvent{SessionID: "session", StreamEvent: agent.StreamEventCommit})
	if snapshots := a.snapshotStreamState(); len(snapshots) != 0 {
		t.Fatalf("commit retained non-authoritative tool call: %+v", snapshots)
	}

	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolCall: &agent.ToolCall{ID: "call-1", Name: "shell", Args: `{"command":"go test"}`},
	}}})

	snapshots := a.snapshotStreamState()
	if len(snapshots) != 1 || snapshots[0].toolArgs != `{"command":"go test"}` || snapshots[0].toolPartial {
		t.Fatalf("definitive call did not replace partial snapshot: %+v", snapshots)
	}
}

func TestStreamCommitStartsFreshTextCell(t *testing.T) {
	a, _ := newStreamTestApp(nil)
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Text: "first", TextID: "message-1",
	}}})
	a.handleTurnEvent(code.TurnEvent{SessionID: "session", StreamEvent: agent.StreamEventCommit})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Text: "second", TextID: "message-1",
	}}})

	snapshots := a.snapshotStreamState()
	if len(snapshots) != 2 || snapshots[0].text != "first" || snapshots[1].text != "second" {
		t.Fatalf("commit did not split text cells: %+v", snapshots)
	}
}

func TestCommittedTextReconcilesByMessageID(t *testing.T) {
	a, testAgent := newStreamTestApp(nil)
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Text: "partial rendering", TextID: "message-1",
	}}})
	testAgent.messages = append(testAgent.messages, agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Text: "authoritative retained text", TextID: "message-1",
	}}})
	a.syncMessages()

	out := ansi.Strip(strings.Join(a.chatViewLines(100), "\n"))
	if strings.Contains(out, "partial rendering") || strings.Count(out, "authoritative retained text") != 1 {
		t.Fatalf("stable text ID did not reconcile live and retained content: %q", out)
	}
}

func TestReleaseToolCellKeepsLiveCellUntilMatchingResult(t *testing.T) {
	a := &App{}
	a.streamCurrent = streamSnapshot{toolID: "call-1", toolName: "shell", toolHint: "ls"}

	a.releaseToolCell(&agent.ToolResult{ID: "call-2", Name: "shell"})
	if a.streamCurrent.toolName == "" {
		t.Fatal("live cell released by a different call's result")
	}

	a.releaseToolCell(&agent.ToolResult{ID: "call-1", Name: "shell"})
	if a.streamCurrent.toolName != "" || a.streamCurrent.toolID != "" {
		t.Fatal("live cell not released by matching result")
	}

	a.streamCurrent = streamSnapshot{toolName: "read"}
	a.releaseToolCell(&agent.ToolResult{Name: "read"})
	if a.streamCurrent.toolName != "" {
		t.Fatal("live cell not released by name fallback")
	}
}

func TestReleaseToolCellRemovesArchivedCompletedTool(t *testing.T) {
	a := &App{queue: make(chan func(), 8), quit: make(chan struct{})}
	result := &agent.ToolResult{ID: "call-1", Name: "read", Args: `{"path":"one.go"}`, Content: "ok"}
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolCall: &agent.ToolCall{ID: result.ID, Name: result.Name, Args: result.Args},
	}}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: result}}})

	if tail := strings.Join(a.streamCells(100), "\n"); !strings.Contains(tail, "one.go") {
		t.Fatalf("completed ACP tool vanished before commit: %q", tail)
	}
	a.releaseToolCell(result)
	if tail := strings.Join(a.streamCells(100), "\n"); strings.Contains(tail, "one.go") {
		t.Fatalf("committed native tool remained duplicated in live tail: %q", tail)
	}
}

func TestParallelArchivedToolReceivesProgressAndCompletion(t *testing.T) {
	a := &App{queue: make(chan func(), 8), quit: make(chan struct{})}
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolCall: &agent.ToolCall{ID: "call-1", Name: "read", Args: `{"path":"one.go"}`},
	}}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolCall: &agent.ToolCall{ID: "call-2", Name: "read", Args: `{"path":"two.go"}`},
	}}})

	a.onToolProgress(context.Background(), "call-1", "halfway")
	if len(a.streamHistory) != 1 || a.streamHistory[0].toolProgress != "halfway" {
		t.Fatalf("archived tool progress was lost: %+v", a.streamHistory)
	}

	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		ToolResult: &agent.ToolResult{ID: "call-1", Name: "read", Content: "ok"},
	}}})
	if a.streamHistory[0].toolResult == nil || a.streamHistory[0].toolResult.Args == "" {
		t.Fatalf("archived tool did not retain its completed result: %+v", a.streamHistory[0])
	}
	if a.streamCurrent.toolID != "call-2" {
		t.Fatalf("completing archived tool disturbed current tool: %+v", a.streamCurrent)
	}
}

func TestAcceptedSteerMovesIntoOrderedLiveTranscript(t *testing.T) {
	a := &App{pendingEcho: []pendingEchoItem{
		{ID: "queued", Text: "do this next", State: code.TurnInputQueued},
	}}
	a.streamCurrent = streamSnapshot{text: "answer before steer"}
	a.appendLiveUserEcho("change direction")
	a.streamCurrent.text = "answer after steer"

	view := strings.Join(a.chatViewLines(100), "\n")
	before := strings.Index(view, "answer before steer")
	steer := strings.Index(view, "change direction")
	after := strings.Index(view, "answer after steer")
	if before < 0 || steer <= before || after <= steer {
		t.Fatalf("accepted steer is not ordered in the live transcript: %q", view)
	}
	if strings.Contains(view, "change direction (steered)") {
		t.Fatalf("accepted steer remained a bottom preview: %q", view)
	}
	if !strings.Contains(view, "do this next (queued)") {
		t.Fatalf("queued follow-up is not identified: %q", view)
	}
}

func TestQueuedEchoMovesIntoTranscriptWhenTurnBecomesActive(t *testing.T) {
	a := &App{pendingEcho: []pendingEchoItem{
		{ID: "follow-up", Text: "do this now", State: code.TurnInputQueued},
	}}
	a.promotePendingEcho("follow-up")
	a.streamCurrent.text = "new answer"

	view := strings.Join(a.chatViewLines(100), "\n")
	user := strings.Index(view, "do this now")
	answer := strings.Index(view, "new answer")
	if user < 0 || answer <= user {
		t.Fatalf("active follow-up is not ordered before its answer: %q", view)
	}
	if strings.Contains(view, "do this now (queued)") {
		t.Fatalf("active follow-up remained a bottom preview: %q", view)
	}
}

func TestActivateSessionResetsTurnState(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	a := &App{agent: codeagent.New(ws, &agent.Config{}, nil), sessionID: "old", sessionEpoch: 3}
	a.phase.Store(int32(PhaseStreaming))
	a.streamCurrent = streamSnapshot{
		text: "partial", reasoning: "thinking", reasoningID: "reasoning", toolName: "shell",
	}

	a.activateSession("new")

	if a.sessionID != "new" || a.sessionEpoch != 4 {
		t.Fatalf("session = %q at epoch %d", a.sessionID, a.sessionEpoch)
	}
	if a.getPhase() != PhaseIdle {
		t.Fatalf("phase = %v", a.getPhase())
	}
	if !a.streamCurrent.empty() {
		t.Fatalf("stream state was retained: text=%q reasoning=%q tool=%q id=%q",
			a.streamCurrent.text, a.streamCurrent.reasoning, a.streamCurrent.toolName, a.streamCurrent.reasoningID)
	}

	called := false
	a.withCurrentSession("old", func() { called = true })
	if called {
		t.Fatal("old session was still current")
	}
	a.withCurrentSession("new", func() { called = true })
	if !called {
		t.Fatal("new session was not current")
	}
}
