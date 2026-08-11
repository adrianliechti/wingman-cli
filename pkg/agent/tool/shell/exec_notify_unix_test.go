//go:build !windows

package shell

import (
	"strings"
	"testing"
	"time"
)

func TestExecExitNotifiesBackgroundedSession(t *testing.T) {
	events := make(chan ExecExit, 1)
	m := NewExecManager(func(e ExecExit) { events <- e })
	defer m.Close()

	tools := ExecTools(m, t.TempDir(), nil, nil, nil)

	out, err := tools[0].Execute(t.Context(), map[string]any{
		"command":     "sleep 0.2; echo done",
		"description": "notify test",
		"wait":        0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "Still running") {
		t.Fatalf("expected backgrounded session, got %q", out.Content)
	}

	select {
	case e := <-events:
		if e.Description != "notify test" {
			t.Fatalf("description = %q", e.Description)
		}
		if e.Failed {
			t.Fatalf("unexpected failure: %s", e.Notice)
		}
		if !strings.Contains(e.Output, "done") {
			t.Fatalf("output = %q", e.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit notification arrived")
	}
}

func TestExecExitInlineDeliverySuppressesNotification(t *testing.T) {
	events := make(chan ExecExit, 1)
	m := NewExecManager(func(e ExecExit) { events <- e })
	defer m.Close()

	tools := ExecTools(m, t.TempDir(), nil, nil, nil)

	if _, err := tools[0].Execute(t.Context(), map[string]any{
		"command": "sleep 0.2; echo done",
		"wait":    0,
	}); err != nil {
		t.Fatal(err)
	}

	out, err := tools[1].Execute(t.Context(), map[string]any{"session_id": 1, "wait": 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "done") || !strings.Contains(out.Content, "Command completed") {
		t.Fatalf("expected inline exit delivery, got %q", out.Content)
	}

	select {
	case e := <-events:
		t.Fatalf("unexpected notification: %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}
