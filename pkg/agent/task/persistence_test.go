package task_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
)

func durableState(text string) agent.State {
	message := agent.Message{Role: agent.RoleUser, Content: []agent.Content{{Text: text}}}
	return agent.State{Events: []agent.RuntimeEvent{{
		Sequence: 1, ID: "message-1", Type: agent.EventMessage, At: time.Now().UTC(), Message: &message,
	}}}
}

func TestFileRegistryRestoresCompletedTaskAndChildLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	r, err := task.NewFileRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"version":1}`)
	launched, err := r.LaunchAgent("agent-identity-123", "inspect", "explore", func(tk *task.Task) error {
		return tk.SetDurableAgent("agent-identity-123", durableState("child context"), raw)
	}, func(context.Context, *task.Task) (string, error) {
		return "finished report", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if event := waitEvent(t, r); event.Status != task.StatusDone {
		t.Fatalf("completion = %#v", event)
	}
	r.Close()

	restored, err := task.NewFileRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got := restored.Get(launched.ID)
	if got == nil || got.Status() != task.StatusDone || got.Result() != "finished report" {
		t.Fatalf("restored task = %#v", got)
	}
	agentID, state, resume := got.DurableAgent()
	if agentID != "agent-identity-123" || string(resume) != string(raw) {
		t.Fatalf("identity=%q resume=%s", agentID, resume)
	}
	if len(state.Events) != 1 || len(got.PeekMessages()) != 1 || got.PeekMessages()[0].Content[0].Text != "child context" {
		t.Fatalf("restored child state = %#v", state)
	}
}

func TestFileRegistryReconcilesRunningTaskWithoutReplaying(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	r, err := task.NewFileRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	launched, err := r.LaunchAgent("agent-running-123", "inspect", "explore", func(tk *task.Task) error {
		return tk.SetDurableAgent("agent-running-123", durableState("partial context"), json.RawMessage(`{"version":1}`))
	}, func(context.Context, *task.Task) (string, error) {
		<-release
		return "should not be replayed", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	restored, err := task.NewFileRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.Get(launched.ID)
	if got == nil || got.Status() != task.StatusFailed {
		t.Fatalf("restored running task = %#v", got)
	}
	select {
	case event := <-restored.Events():
		if event.ID != launched.ID || event.Status != task.StatusFailed {
			t.Fatalf("reconciliation event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing interrupted-task notification")
	}
	if len(got.PeekMessages()) != 1 {
		t.Fatal("partial child context was lost")
	}

	restored.Close()
	close(release)
	_ = waitEvent(t, r)
	r.Close()
}
