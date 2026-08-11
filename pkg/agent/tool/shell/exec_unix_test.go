//go:build !windows

package shell

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestExecCommandCompletes(t *testing.T) {
	m := NewExecManager(nil)
	defer m.Close()

	out, err := executeExecCommand(context.Background(), m, t.TempDir(), nil, NewApprovals(), nil, map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "hello") {
		t.Fatalf("output = %q", out.Content)
	}
	if !strings.Contains(out.Content, "Command completed") {
		t.Fatalf("output = %q", out.Content)
	}
	if len(m.sessions) != 0 {
		t.Fatal("expected no running sessions")
	}
}

func TestExecCommandExitCode(t *testing.T) {
	m := NewExecManager(nil)
	defer m.Close()

	out, err := executeExecCommand(context.Background(), m, t.TempDir(), nil, NewApprovals(), nil, map[string]any{
		"command": "exit 3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "Command exited with code 3") || !out.IsError || out.Metadata["exit_code"] != 3 {
		t.Fatalf("output = %+v", out)
	}
}

func TestExecCommandBackgroundPollKill(t *testing.T) {
	m := NewExecManager(nil)
	defer m.Close()

	ctx := context.Background()

	out, err := executeExecCommand(ctx, m, t.TempDir(), nil, NewApprovals(), nil, map[string]any{
		"command": "echo started; sleep 30",
		"wait":    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "started") || !strings.Contains(out.Content, "session_id 1") {
		t.Fatalf("output = %q", out.Content)
	}

	out, err = executeExecSession(ctx, m, nil, NewApprovals(), map[string]any{"session_id": 1, "wait": 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "no new output") {
		t.Fatalf("output = %q", out.Content)
	}

	out, err = executeExecSession(ctx, m, nil, NewApprovals(), map[string]any{"session_id": 1, "kill": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "Session 1 killed") {
		t.Fatalf("output = %q", out.Content)
	}

	if _, err := executeExecSession(ctx, m, nil, NewApprovals(), map[string]any{"session_id": 1}); err == nil {
		t.Fatal("expected error for removed session")
	}
}

func TestExecSessionStdin(t *testing.T) {
	m := NewExecManager(nil)
	defer m.Close()

	ctx := context.Background()

	out, err := executeExecCommand(ctx, m, t.TempDir(), nil, NewApprovals(), nil, map[string]any{
		"command": "cat",
		"wait":    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "session_id 1") {
		t.Fatalf("output = %q", out.Content)
	}

	out, err = executeExecSession(ctx, m, nil, NewApprovals(), map[string]any{"session_id": 1, "input": "hello\n", "wait": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "hello") {
		t.Fatalf("output = %q", out.Content)
	}

	out, err = executeExecSession(ctx, m, nil, NewApprovals(), map[string]any{"session_id": 1, "eof": true, "wait": 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "Command completed") {
		t.Fatalf("output = %q", out.Content)
	}
}

func TestExecCommandTTY(t *testing.T) {
	m := NewExecManager(nil)
	defer m.Close()

	ctx := context.Background()

	out, err := executeExecCommand(ctx, m, t.TempDir(), nil, NewApprovals(), nil, map[string]any{
		"command": "[ -t 0 ] && echo isatty || echo notty",
		"tty":     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "isatty") {
		t.Fatalf("tty output = %q", out.Content)
	}

	out, err = executeExecCommand(ctx, m, t.TempDir(), nil, NewApprovals(), nil, map[string]any{
		"command": "[ -t 0 ] && echo isatty || echo notty",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "notty") {
		t.Fatalf("pipe output = %q", out.Content)
	}
}

func TestExecSessionTTYStdinEOF(t *testing.T) {
	m := NewExecManager(nil)
	defer m.Close()

	ctx := context.Background()

	out, err := executeExecCommand(ctx, m, t.TempDir(), nil, NewApprovals(), nil, map[string]any{
		"command": "cat",
		"tty":     true,
		"wait":    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "session_id 1") {
		t.Fatalf("output = %q", out.Content)
	}

	out, err = executeExecSession(ctx, m, nil, NewApprovals(), map[string]any{"session_id": 1, "input": "hello\n", "wait": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "hello") {
		t.Fatalf("output = %q", out.Content)
	}

	out, err = executeExecSession(ctx, m, nil, NewApprovals(), map[string]any{"session_id": 1, "eof": true, "wait": 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "Command completed") {
		t.Fatalf("output = %q", out.Content)
	}
}

func TestExecSessionDangerousInputConfirmed(t *testing.T) {
	m := NewExecManager(nil)
	defer m.Close()

	ctx := context.Background()
	confirmCalls := 0

	elicit := &tool.Elicitation{
		Confirm: func(ctx context.Context, message string) (bool, error) {
			confirmCalls++
			return false, nil
		},
	}
	appr := NewApprovals()

	out, err := executeExecCommand(ctx, m, t.TempDir(), elicit, appr, nil, map[string]any{
		"command": "cat",
		"wait":    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "session_id 1") {
		t.Fatalf("output = %q", out.Content)
	}

	_, err = executeExecSession(ctx, m, elicit, appr, map[string]any{"session_id": 1, "input": "sudo rm -rf /tmp/x\n"})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("dangerous stdin not denied: %v", err)
	}
	if confirmCalls != 1 {
		t.Fatalf("confirm called %d times, want 1", confirmCalls)
	}

	if _, err := executeExecSession(ctx, m, elicit, appr, map[string]any{"session_id": 1, "input": "hello\n", "wait": 1}); err != nil {
		t.Fatalf("benign stdin failed: %v", err)
	}
	if confirmCalls != 1 {
		t.Fatalf("benign stdin prompted (confirm called %d times)", confirmCalls)
	}
}

func TestExecSessionCtrlCInterruptsPipeSession(t *testing.T) {
	m := NewExecManager(nil)
	defer m.Close()

	ctx := context.Background()

	out, err := executeExecCommand(ctx, m, t.TempDir(), nil, NewApprovals(), nil, map[string]any{
		"command": "sleep 30",
		"wait":    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "session_id 1") {
		t.Fatalf("output = %q", out.Content)
	}

	out, err = executeExecSession(ctx, m, nil, NewApprovals(), map[string]any{"session_id": 1, "input": "\u0003", "wait": 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "signal: interrupt") {
		t.Fatalf("output = %q", out.Content)
	}
}

func TestExecSessionBufferCap(t *testing.T) {
	s := &execSession{done: make(chan struct{})}

	s.Write(bytes.Repeat([]byte("a"), maxUnreadBytes+1000))

	out := s.drain()
	if !strings.Contains(out, "1000 bytes of earlier output dropped") {
		t.Fatalf("missing drop marker: %q", out[:80])
	}
	if len(out) > maxUnreadBytes+100 {
		t.Fatalf("drained %d bytes", len(out))
	}

	if s.drain() != "" {
		t.Fatal("expected empty buffer after drain")
	}
}
