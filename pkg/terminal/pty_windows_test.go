//go:build windows

package terminal

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestWindowsConPTYRunsCommand starts a real command under the ConPTY backend
// and checks that output flows back and the process exits. It is the Windows
// counterpart of the Unix workspace-PTY test.
func TestWindowsConPTYRunsCommand(t *testing.T) {
	if !Supported() {
		t.Skip("pty not supported")
	}

	root := t.TempDir()
	manager := NewManager(root)
	defer manager.Close()

	marker := "wingman-conpty-ok"
	session, err := manager.CreateCommand(CommandSpec{
		Path:  os.Getenv("ComSpec"),
		Args:  []string{"/c", "echo " + marker},
		Dir:   root,
		Title: "ConPTY command",
	}, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if session.ProcessID() <= 0 {
		t.Fatalf("process id = %d, want > 0", session.ProcessID())
	}

	snapshot, output, cancel := session.Subscribe()
	defer cancel()

	var builder strings.Builder
	builder.Write(snapshot)

	deadline := time.After(15 * time.Second)
	for !strings.Contains(builder.String(), marker) {
		select {
		case chunk, ok := <-output:
			if !ok {
				if strings.Contains(builder.String(), marker) {
					return
				}
				t.Fatalf("output closed before marker; got %q", builder.String())
			}
			builder.Write(chunk)
		case <-deadline:
			t.Fatalf("timed out waiting for marker; got %q", builder.String())
		}
	}

	select {
	case <-session.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("session did not exit after command completed")
	}
}
