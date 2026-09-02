package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func runExec(t *testing.T, command string) tool.Result {
	t.Helper()
	manager := NewExecManager(nil)
	t.Cleanup(manager.Close)
	result, err := ExecTools(manager, t.TempDir(), nil, nil, nil)[0].Execute(context.Background(), map[string]any{
		"command": command,
		"wait":    10,
	})
	if err != nil {
		t.Fatalf("exec_command failed: %v", err)
	}
	return result
}

func TestExecCommandHandlesShellScriptsAndMergedOutput(t *testing.T) {
	result := runExec(t, `x=hello; for y in world again; do echo "$x $y"; done; echo stderr >&2`)
	for _, want := range []string{"hello world", "hello again", "stderr", "Command completed"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("output %q does not contain %q", result.Content, want)
		}
	}
}

func TestExecCommandReportsEmptyOutputAndStructuredFailure(t *testing.T) {
	empty := runExec(t, "true")
	if empty.Content != "Command completed" || empty.IsError {
		t.Fatalf("empty command result = %+v", empty)
	}

	failed := runExec(t, "exit 7")
	if !failed.IsError || failed.Metadata["exit_code"] != 7 || !strings.Contains(failed.Content, "code 7") {
		t.Fatalf("failed command result = %+v", failed)
	}
}

func TestExecCommandWorkdirAndEnvironment(t *testing.T) {
	workDir := t.TempDir()
	sub := filepath.Join(workDir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	manager := NewExecManager(nil)
	defer manager.Close()
	execTool := ExecTools(manager, workDir, nil, nil, nil)[0]
	result, err := execTool.Execute(context.Background(), map[string]any{
		"command": "printf '%s|%s' \"$PWD\" \"$GIT_EDITOR\"",
		"workdir": "sub",
		"wait":    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "/sub|true") {
		t.Fatalf("unexpected workdir/environment output: %q", result.Content)
	}
	if _, err := execTool.Execute(context.Background(), map[string]any{"command": "pwd", "workdir": "missing"}); err == nil {
		t.Fatal("expected an inaccessible-workdir error")
	}
}

func TestExecCommandElicitsOnlyForDangerousCommands(t *testing.T) {
	workDir := t.TempDir()
	confirmCalls := 0
	elicit := &tool.Elicitation{Confirm: func(context.Context, string) (bool, error) {
		confirmCalls++
		return false, nil
	}}
	manager := NewExecManager(nil)
	defer manager.Close()
	execTool := ExecTools(manager, workDir, elicit, nil, nil)[0]

	if _, err := execTool.Execute(context.Background(), map[string]any{"command": "printf hi > out.txt", "wait": 10}); err != nil {
		t.Fatalf("benign command failed: %v", err)
	}
	if confirmCalls != 0 {
		t.Fatalf("benign command prompted %d times", confirmCalls)
	}
	if _, err := execTool.Execute(context.Background(), map[string]any{"command": "rm -rf out.txt", "wait": 10}); err == nil {
		t.Fatal("dangerous command was not denied")
	}
	if confirmCalls != 1 {
		t.Fatalf("dangerous command prompted %d times", confirmCalls)
	}
}

func TestExecCommandTimeoutKillsTheProcess(t *testing.T) {
	manager := NewExecManager(nil)
	defer manager.Close()

	started := time.Now()
	result, err := ExecTools(manager, t.TempDir(), nil, nil, nil)[0].Execute(context.Background(), map[string]any{
		"command": "sleep 30",
		"timeout": 1,
		"wait":    20,
	})
	if err != nil {
		t.Fatalf("exec_command failed: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("timeout did not kill the command promptly (took %s)", elapsed)
	}
	if !strings.Contains(result.Content, "exceeding its 1s timeout") {
		t.Fatalf("result does not report the timeout: %q", result.Content)
	}
}

func TestExecCommandBackgroundsOnceOutputGoesQuiet(t *testing.T) {
	restore := execIdleGrace
	execIdleGrace = 150 * time.Millisecond
	defer func() { execIdleGrace = restore }()

	manager := NewExecManager(nil)
	defer manager.Close()
	execTool := ExecTools(manager, t.TempDir(), nil, nil, nil)[0]

	started := time.Now()
	result, err := execTool.Execute(context.Background(), map[string]any{
		"command": "echo listening; sleep 30",
		"wait":    20,
	})
	if err != nil {
		t.Fatalf("exec_command failed: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("idle command was not backgrounded promptly (took %s)", elapsed)
	}
	for _, want := range []string{"listening", "Started and idle", "session_id"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("output %q does not contain %q", result.Content, want)
		}
	}

	// A command that has printed nothing is still working, so the idle grace
	// must not background it before it finishes.
	quiet, err := execTool.Execute(context.Background(), map[string]any{
		"command": "sleep 1; echo done",
		"wait":    20,
	})
	if err != nil {
		t.Fatalf("exec_command failed: %v", err)
	}
	if !strings.Contains(quiet.Content, "done") || !strings.Contains(quiet.Content, "Command completed") {
		t.Fatalf("silent command was backgrounded instead of completing: %q", quiet.Content)
	}
}

func TestSanitizeOutput(t *testing.T) {
	in := "\x1b[?2026h\x1b[2Kprogress 50%\rprogress 100%\nplain\r\n"
	if got := sanitizeOutput(in); got != "progress 100%\nplain\n" {
		t.Fatalf("sanitized output = %q", got)
	}
}
