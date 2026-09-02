package claude

import (
	"context"
	"testing"
	"time"
)

func TestStoreSessionClosesReplacedSession(t *testing.T) {
	a := New(Options{})
	old := a.newSession("session-1", t.TempDir(), "default", "", nil)
	oldCtx, cancelOld := context.WithCancel(context.Background())
	old.mu.Lock()
	old.cancel = cancelOld
	old.mu.Unlock()
	a.storeSession(old)

	replacement := a.newSession(old.id, old.cwd, "default", "", nil)
	a.storeSession(replacement)
	select {
	case <-oldCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("replaced session was not cancelled")
	}
	if !old.isClosed() || a.lookup(old.id) != replacement {
		t.Fatal("replacement session was not installed cleanly")
	}

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if !replacement.isClosed() || a.lookup(old.id) != nil {
		t.Fatal("agent close did not remove and close its sessions")
	}
}
