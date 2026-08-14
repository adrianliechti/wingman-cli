package lsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetSessionStopsRestartingAfterRepeatedCrashes(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	project := projectRoot{
		Dir:    root,
		Server: Server{Name: "test-server", Command: filepath.Join(root, "missing-language-server")},
	}
	key := projectKey(project)

	// Each round simulates a session that started and then died.
	for attempt := 1; attempt <= maxRestarts; attempt++ {
		manager.sessions[key] = &Session{}
		if _, err := manager.getSession(context.Background(), project); err == nil {
			t.Fatalf("attempt %d: expected the restart to fail", attempt)
		}
		if manager.restarts[key] != attempt {
			t.Fatalf("attempt %d: restarts = %d, want %d", attempt, manager.restarts[key], attempt)
		}
	}

	manager.sessions[key] = &Session{}
	_, err := manager.getSession(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "not restarting") {
		t.Fatalf("err = %v, want the restart cap to apply", err)
	}

	// The cap must survive the dead session being dropped from the map.
	if _, err := manager.getSession(context.Background(), project); err == nil || !strings.Contains(err.Error(), "not restarting") {
		t.Fatalf("err = %v, want the restart cap to stay in effect", err)
	}
}
