package code

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
)

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

	a.onToolProgress("call-1", "halfway")
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

func TestPendingEchoDistinguishesSteeredFromQueuedInput(t *testing.T) {
	a := &App{pendingEcho: []pendingEchoItem{
		{ID: "steered", Text: "change direction", State: code.TurnInputSteered},
		{ID: "queued", Text: "do this next", State: code.TurnInputQueued},
	}}
	view := strings.Join(a.chatViewLines(100), "\n")
	if !strings.Contains(view, "change direction (steered)") {
		t.Fatalf("accepted steer is not identified: %q", view)
	}
	if !strings.Contains(view, "do this next (queued)") {
		t.Fatalf("queued follow-up is not identified: %q", view)
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
