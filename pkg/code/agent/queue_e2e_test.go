package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	harness "github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/session"
)

var _ code.TurnQueueStore = (*Agent)(nil)

func queueState(texts ...string) code.TurnQueueState {
	state := code.TurnQueueState{}
	for _, text := range texts {
		state.Inputs = append(state.Inputs, code.TurnInput{
			ID: text, Intent: code.TurnInputFollowUp, Content: []harness.Content{{Text: text}},
		})
	}
	return state
}

func TestTurnQueueIsolatesSessions(t *testing.T) {
	a := &Agent{sessionsDir: t.TempDir()}

	if err := a.SaveTurnQueue("alpha", queueState("alpha work")); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveTurnQueue("beta", queueState("beta work")); err != nil {
		t.Fatal(err)
	}

	alpha, err := a.LoadTurnQueue("alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := a.LoadTurnQueue("beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha.Inputs) != 1 || alpha.Inputs[0].Content[0].Text != "alpha work" {
		t.Fatalf("alpha queue = %#v", alpha)
	}
	if len(beta.Inputs) != 1 || beta.Inputs[0].Content[0].Text != "beta work" {
		t.Fatalf("beta queue = %#v", beta)
	}

	missing, err := a.LoadTurnQueue("never-used")
	if err != nil {
		t.Fatalf("unknown session should load an empty queue: %v", err)
	}
	if len(missing.Inputs) != 0 || missing.Paused {
		t.Fatalf("unknown session queue = %#v", missing)
	}
}

func TestTurnQueueSurvivesInterruptedWrite(t *testing.T) {
	a := &Agent{sessionsDir: t.TempDir()}
	const sessionID = "interrupted"

	if err := a.SaveTurnQueue(sessionID, queueState("survivor")); err != nil {
		t.Fatal(err)
	}

	dir, err := session.ArtifactDir(a.sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, turnQueueFile+".tmp-abandoned"), []byte("{partial"), 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := a.LoadTurnQueue(sessionID)
	if err != nil {
		t.Fatalf("abandoned temp file broke the queue: %v", err)
	}
	if len(loaded.Inputs) != 1 || loaded.Inputs[0].Content[0].Text != "survivor" {
		t.Fatalf("queue after interrupted write = %#v", loaded)
	}
}

func TestTurnQueueOverwriteShrinksAndClears(t *testing.T) {
	a := &Agent{sessionsDir: t.TempDir()}
	const sessionID = "shrinking"

	if err := a.SaveTurnQueue(sessionID, queueState("one", "two", "three")); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveTurnQueue(sessionID, queueState("two")); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.LoadTurnQueue(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Inputs) != 1 || loaded.Inputs[0].ID != "two" {
		t.Fatalf("shrunk queue = %#v", loaded)
	}

	if err := a.SaveTurnQueue(sessionID, code.TurnQueueState{}); err != nil {
		t.Fatal(err)
	}
	cleared, err := a.LoadTurnQueue(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.Inputs) != 0 || cleared.Paused {
		t.Fatalf("cleared queue = %#v", cleared)
	}
}

func TestTurnQueueCorruptFileReportsAndRecovers(t *testing.T) {
	a := &Agent{sessionsDir: t.TempDir()}
	const sessionID = "corrupt"

	if err := a.SaveTurnQueue(sessionID, queueState("original")); err != nil {
		t.Fatal(err)
	}
	dir, err := session.ArtifactDir(a.sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, turnQueueFile)
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.LoadTurnQueue(sessionID); err == nil {
		t.Fatal("corrupt queue file loaded without error")
	} else if !strings.Contains(err.Error(), "parse turn queue") {
		t.Fatalf("corrupt queue error = %v, want a parse-turn-queue error", err)
	}

	if err := a.SaveTurnQueue(sessionID, queueState("rewritten")); err != nil {
		t.Fatalf("save could not repair a corrupt queue file: %v", err)
	}
	repaired, err := a.LoadTurnQueue(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired.Inputs) != 1 || repaired.Inputs[0].ID != "rewritten" {
		t.Fatalf("repaired queue = %#v", repaired)
	}
}

func TestTurnQueueRejectsSessionIDEscapingArtifactDir(t *testing.T) {
	a := &Agent{sessionsDir: t.TempDir()}

	if err := a.SaveTurnQueue("../escape", queueState("nope")); err == nil {
		t.Fatal("save accepted a session ID that escapes the sessions directory")
	}
	if _, err := a.LoadTurnQueue("../escape"); err == nil {
		t.Fatal("load accepted a session ID that escapes the sessions directory")
	}
}
