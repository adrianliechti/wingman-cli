package dap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/terminal"
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

func TestLiveDelveIntegratedTerminal(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real Delve session")
	}
	if resolveAdapterCommand("dlv") == "" {
		t.Skip("dlv is not installed")
	}

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/dapterminal\n\ngo 1.24\n")
	writeTestFile(t, filepath.Join(root, "main.go"), `package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Print("value> ")
	value, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Printf("received=%s\n", strings.TrimSpace(value))
}
`)

	terminals := terminal.NewManager(root)
	defer terminals.Close()
	manager := NewManager(root)
	manager.SetTerminalLauncher(liveTerminalLauncher{manager: terminals})
	defer manager.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session, err := manager.Start(ctx, StartOptions{
		Console: ConsoleIntegrated,
		Configuration: map[string]any{
			"mode":    "debug",
			"program": filepath.Join(root, "main.go"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.TerminalID == "" || status.Console != ConsoleIntegrated {
		t.Fatalf("status = %+v", status)
	}
	terminalSession := terminals.Get(status.TerminalID)
	if terminalSession == nil {
		t.Fatalf("terminal %q is not registered", status.TerminalID)
	}
	if !waitForSessionOutput(ctx, session, "value>") {
		t.Fatalf("interactive prompt not observed:\n%s", session.Output())
	}
	if err := terminalSession.Write([]byte("wingman\r")); err != nil {
		t.Fatal(err)
	}
	if !waitForSessionOutput(ctx, session, "received=wingman") {
		t.Fatalf("interactive response not observed:\n%s", session.Output())
	}
	if !waitForSessionState(ctx, session, StateTerminated) {
		t.Fatalf("session did not terminate after the debuggee exited: %+v", session.Status())
	}
	if !waitForTerminalRemoval(ctx, terminals, status.TerminalID) {
		t.Fatalf("debug adapter terminal %q remained open", status.TerminalID)
	}
}

type liveTerminalLauncher struct {
	manager *terminal.Manager
}

func (launcher liveTerminalLauncher) LaunchTerminal(_ context.Context, launch TerminalLaunch) (TerminalProcess, error) {
	return launcher.manager.CreateCommand(terminal.CommandSpec{
		Path: launch.Path, Args: launch.Args, Dir: launch.Dir, Env: launch.Env, Title: launch.Title,
	}, terminal.DefaultCols, terminal.DefaultRows)
}

func waitForSessionOutput(ctx context.Context, session *Session, marker string) bool {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.Contains(session.Output(), marker) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func waitForSessionState(ctx context.Context, session *Session, state State) bool {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if session.Status().State == state {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func waitForTerminalRemoval(ctx context.Context, manager *terminal.Manager, id string) bool {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if manager.Get(id) == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
