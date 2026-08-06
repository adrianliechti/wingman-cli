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
	a.currentToolID = "call-1"
	a.currentToolName = "shell"
	a.currentToolHint = "ls"

	a.releaseToolCell(&agent.ToolResult{ID: "call-2", Name: "shell"})
	if a.currentToolName == "" {
		t.Fatal("live cell released by a different call's result")
	}

	a.releaseToolCell(&agent.ToolResult{ID: "call-1", Name: "shell"})
	if a.currentToolName != "" || a.currentToolID != "" {
		t.Fatal("live cell not released by matching result")
	}

	a.currentToolID = ""
	a.currentToolName = "read"
	a.releaseToolCell(&agent.ToolResult{Name: "read"})
	if a.currentToolName != "" {
		t.Fatal("live cell not released by name fallback")
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
	a.streamingText = "partial"
	a.streamingReasoning = "thinking"
	a.currentToolName = "shell"
	a.reasoningID = "reasoning"

	a.activateSession("new")

	if a.sessionID != "new" || a.sessionEpoch != 4 {
		t.Fatalf("session = %q at epoch %d", a.sessionID, a.sessionEpoch)
	}
	if a.getPhase() != PhaseIdle {
		t.Fatalf("phase = %v", a.getPhase())
	}
	if a.streamingText != "" || a.streamingReasoning != "" || a.currentToolName != "" || a.reasoningID != "" {
		t.Fatalf("stream state was retained: text=%q reasoning=%q tool=%q id=%q",
			a.streamingText, a.streamingReasoning, a.currentToolName, a.reasoningID)
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
