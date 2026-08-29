package agent

import (
	"testing"

	harness "github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func TestTurnQueueFileRoundTripOwnsContent(t *testing.T) {
	a := &Agent{sessionsDir: t.TempDir()}
	state := code.TurnQueueState{Paused: true, Inputs: []code.TurnInput{{
		ID: "queued", Intent: code.TurnInputFollowUp, Content: []harness.Content{{Text: "persist me"}},
	}}}
	if err := a.SaveTurnQueue("session", state); err != nil {
		t.Fatal(err)
	}
	state.Inputs[0].Content[0].Text = "mutated caller"

	loaded, err := a.LoadTurnQueue("session")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Paused || len(loaded.Inputs) != 1 || loaded.Inputs[0].Content[0].Text != "persist me" {
		t.Fatalf("loaded queue = %#v", loaded)
	}
}
