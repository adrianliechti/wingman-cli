package lsp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
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

func TestServerInitializationOptionsInvalidateOldDescriptor(t *testing.T) {
	manager := NewManager(t.TempDir())
	server := Server{Name: "jdtls", InitializationOptions: []byte(`{"bundles":[]}`)}

	if err := manager.SetServerInitializationOptions("JDTLS", map[string]any{"bundles": []string{"debug.jar"}}); err != nil {
		t.Fatal(err)
	}
	manager.detectMu.Lock()
	current := manager.serverInitializationOptionsCurrentLocked(server)
	manager.detectMu.Unlock()
	if current {
		t.Fatal("descriptor with old initialization options remained current")
	}
	if len(manager.initializationOptions["jdtls"]) == 0 {
		t.Fatal("initialization options were not normalized by server name")
	}
}

func TestRetryRPCReturnsLastTransientErrorWithoutAnotherDelay(t *testing.T) {
	previousDelay := retryBaseDelay
	retryBaseDelay = 0
	t.Cleanup(func() { retryBaseDelay = previousDelay })

	ctx, cancel := context.WithCancel(context.Background())
	want := &jsonrpc2.Error{Code: codeRequestCancelled, Message: "retry"}
	attempts := 0
	_, err := retryRPC(ctx, func() (struct{}, error) {
		attempts++
		if attempts == maxRetries {
			// Cancellation after the final response must not replace that response
			// while retryRPC waits for a retry it will never perform.
			cancel()
		}
		return struct{}{}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("retry error = %v, want final transient error %v", err, want)
	}
	if attempts != maxRetries {
		t.Fatalf("attempts = %d, want %d", attempts, maxRetries)
	}
}

func TestManagerCloseCancelsInFlightSessionStart(t *testing.T) {
	manager := NewManager(t.TempDir())
	started := make(chan struct{})
	manager.connect = func(ctx context.Context, _ string, _ Server) (*Session, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	project := projectRoot{Dir: t.TempDir(), Server: Server{Name: "blocked"}}
	result := make(chan error, 1)
	go func() {
		_, err := manager.getSession(context.Background(), project)
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session start did not begin")
	}
	manager.Close()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "manager is closed") {
			t.Fatalf("getSession error = %v, want manager closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager close left session startup blocked")
	}
}
