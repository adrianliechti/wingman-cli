package dap

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

	manager := NewManager(root, liveDelveAdapter())
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	session, err := manager.Start(ctx, StartOptions{
		Configuration: map[string]any{
			"mode":    "debug",
			"program": filepath.Join(root, "main.go"),
		},
		Breakpoints: map[string][]SourceBreakpoint{
			filepath.Join(root, "main.go"): {{Line: 6}},
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
	scopes, err := session.Scopes(ctx, frames[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	variables := loadLiveVariables(t, ctx, session, scopes)
	if !slices.ContainsFunc(variables, func(variable Variable) bool {
		return variable.Name == "value" && variable.Value == "41"
	}) {
		t.Fatalf("Delve did not return value=41: scopes=%+v variables=%+v", scopes, variables)
	}
	if err := manager.Stop(ctx, session.ID()); err != nil {
		t.Fatalf("stop Delve session: %v", err)
	}
	requireNoDebugArtifacts(t, root)
}

func loadLiveVariables(t *testing.T, ctx context.Context, session *Session, scopes []Scope) []Variable {
	t.Helper()
	var variables []Variable
	for _, scope := range scopes {
		if scope.VariablesReference <= 0 {
			continue
		}
		values, err := session.Variables(ctx, scope.VariablesReference, 0, 200)
		if err != nil {
			t.Fatalf("load %q variables: %v", scope.Name, err)
		}
		variables = append(variables, values...)
	}
	return variables
}

// TestLiveDelveWorkspacePackage protects deterministic plans that use a
// project-relative package directory without a leading "./".
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
	manager := NewManager(root, liveDelveAdapter())
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
			filepath.Join(root, "cmd", "wingman", "main.go"): {{Line: 23}},
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
	scopes, err := session.Scopes(ctx, frames[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	variables := loadLiveVariables(t, ctx, session, scopes)
	if !slices.ContainsFunc(variables, func(variable Variable) bool {
		return variable.Name == "args" && strings.Contains(variable.Value, "len: 1")
	}) {
		t.Fatalf("Delve did not return args: scopes=%+v variables=%+v", scopes, variables)
	}
}

func TestLiveDelveTerminal(t *testing.T) {
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
	manager := NewManager(root, liveDelveAdapter())
	manager.SetTerminalLauncher(liveTerminalLauncher{manager: terminals})
	defer manager.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session, err := manager.Start(ctx, StartOptions{
		IO: IOTerminal,
		Configuration: map[string]any{
			"mode":    "debug",
			"program": filepath.Join(root, "main.go"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.TerminalID == "" || status.IO != IOTerminal {
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
	requireNoDebugArtifacts(t, root)
}

func TestLiveDelveTerminalManagerCloseCleansProcess(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real Delve session")
	}
	if resolveAdapterCommand("dlv") == "" {
		t.Skip("dlv is not installed")
	}

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/dapclose\n\ngo 1.24\n")
	writeTestFile(t, filepath.Join(root, "main.go"), `package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	fmt.Print("waiting> ")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
`)

	terminals := terminal.NewManager(root)
	defer terminals.Close()
	manager := NewManager(root, liveDelveAdapter())
	manager.SetTerminalLauncher(liveTerminalLauncher{manager: terminals})
	defer manager.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session, err := manager.Start(ctx, StartOptions{
		IO: IOTerminal,
		Configuration: map[string]any{
			"mode":    "debug",
			"program": filepath.Join(root, "main.go"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	terminalID := session.Status().TerminalID
	if terminalID == "" || !waitForSessionOutput(ctx, session, "waiting>") {
		t.Fatalf("terminal session did not start: status=%+v output=%q", session.Status(), session.Output())
	}

	manager.Close()
	if !waitForTerminalRemoval(ctx, terminals, terminalID) {
		t.Fatalf("manager close left Delve terminal %q running", terminalID)
	}
	requireNoDebugArtifacts(t, root)
}

type liveTerminalLauncher struct {
	manager *terminal.Manager
}

func liveDelveAdapter() AdapterDescriptor {
	return AdapterDescriptor{
		Name: "delve", Language: "Go", AdapterID: "go", Command: "dlv",
		Args: []string{"dap", "--listen=127.0.0.1:0"}, Transport: TransportTCP,
		ReadyPrefix: "DAP server listening at:", Markers: []string{"go.mod", "go.work"},
		ConfigurationPaths: []ConfigurationPath{{Key: "program"}, {Key: "cwd", Directory: true}},
		TerminalStrategy:   TerminalAdapterProcess,
		IOConfigKey:        "outputMode",
		IOValues:           map[IOMode]string{IOOutput: "remote", IOTerminal: "local"},
	}
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

func requireNoDebugArtifacts(t *testing.T, root string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		matches, err := filepath.Glob(filepath.Join(root, "__debug_bin*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Delve left debug binaries behind: %v", matches)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
