package dap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveDelveLaunch(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real Delve session")
	}
	if resolveAdapterCommand("dlv") == "" {
		t.Skip("dlv is not installed")
	}

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/daptest\n\ngo 1.24\n")
	writeTestFile(t, filepath.Join(root, "main.go"), `package main

import "fmt"

func work(value int) int {
	return value + 1
}

func main() {
	fmt.Println(work(41))
}
`)

	manager := NewManager(root)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	session, err := manager.Start(ctx, StartOptions{
		Configuration: map[string]any{
			"mode":    "debug",
			"program": filepath.Join(root, "main.go"),
		},
		Breakpoints: map[string][]SourceBreakpoint{
			filepath.Join(root, "main.go"): {{Line: 10}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.State != StateStopped {
		status, _ = session.WaitForStop(ctx, session.StateEpoch())
	}
	if status.State != StateStopped {
		t.Fatalf("status = %+v\noutput:\n%s", status, session.Output())
	}
	frames, _, err := session.StackTrace(ctx, 0, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("Delve returned no stack frames")
	}
}

// TestLiveDelveWorkspacePackage protects the AI-launcher case where a model
// supplies a project-relative package directory without a leading "./".
func TestLiveDelveWorkspacePackage(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real Delve session")
	}
	if resolveAdapterCommand("dlv") == "" {
		t.Skip("dlv is not installed")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(root)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	session, err := manager.Start(ctx, StartOptions{
		ProjectDir: root,
		Configuration: map[string]any{
			"cwd":     ".",
			"mode":    "debug",
			"program": "cmd/wingman",
			"args":    []string{"--help"},
		},
		Breakpoints: map[string][]SourceBreakpoint{
			filepath.Join(root, "cmd", "wingman", "main.go"): {{Line: 18}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.State != StateStopped {
		status, _ = session.WaitForStop(ctx, session.StateEpoch())
	}
	if status.State != StateStopped {
		t.Fatalf("status = %+v\noutput:\n%s", status, session.Output())
	}
}
