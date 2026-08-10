package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const (
	defaultTimeout = 120
	maxOutputBytes = 16 * 1024 * 1024
)

var errCommandTimeout = errors.New("command timeout")

type cappedBuffer struct {
	buf     bytes.Buffer
	dropped int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := maxOutputBytes - b.buf.Len(); remaining > 0 {
		n := min(len(p), remaining)
		b.buf.Write(p[:n])
		b.dropped += len(p) - n
	} else {
		b.dropped += len(p)
	}
	return len(p), nil
}

func (b *cappedBuffer) result() string {
	out := b.buf.String()
	if b.dropped > 0 {
		out += fmt.Sprintf("\n\n[output capped at %dMB; %d further bytes dropped]", maxOutputBytes/(1024*1024), b.dropped)
	}
	return out
}

const (
	progressInterval = 500 * time.Millisecond
	maxProgressTail  = 4096
	maxProgressLine  = 160
)

// progressBuffer captures command output and reports the newest complete
// non-blank line as display-only progress. os/exec serializes Write calls when
// stdout and stderr share one writer, so no locking is needed.
type progressBuffer struct {
	cappedBuffer
	report func(string)

	partial []byte
	lastAt  time.Time
}

func (b *progressBuffer) Write(p []byte) (int, error) {
	b.cappedBuffer.Write(p)

	if b.report == nil {
		return len(p), nil
	}

	b.partial = append(b.partial, p...)
	if over := len(b.partial) - maxProgressTail; over > 0 {
		b.partial = b.partial[over:]
	}

	idx := bytes.LastIndexByte(b.partial, '\n')
	if idx < 0 || time.Since(b.lastAt) < progressInterval {
		return len(p), nil
	}

	if line := lastNonBlankLine(b.partial[:idx]); line != "" {
		b.lastAt = time.Now()
		b.report(line)
	}
	b.partial = b.partial[idx+1:]

	return len(p), nil
}

func lastNonBlankLine(data []byte) string {
	for len(data) > 0 {
		idx := bytes.LastIndexByte(data, '\n')
		line := strings.TrimSpace(string(data[idx+1:]))
		if line != "" {
			if runes := []rune(line); len(runes) > maxProgressLine {
				line = string(runes[:maxProgressLine])
			}
			return line
		}
		if idx < 0 {
			break
		}
		data = data[:idx]
	}
	return ""
}

func safetyGuardLine(elicit *tool.Elicitation) string {
	if elicit == nil || elicit.Confirm == nil {
		return "- There is NO confirmation gate: commands run immediately. Never run destructive or privilege-escalating commands (recursive deletes, sudo, force-push) unless the user explicitly asked for that exact action."
	}
	return "- Safety guard: routine mutating commands run directly, but destructive or privilege-escalating commands require user confirmation first. An approved command re-runs without re-asking for the rest of the session."
}

func Tools(workDir string, extraWritableRoots []string, elicit *tool.Elicitation, appr *Approvals) []tool.Tool {
	if appr == nil {
		appr = NewApprovals()
	}

	description := strings.Join([]string{
		fmt.Sprintf("Execute a command in the host shell (the user's `$SHELL`/`/bin/sh` on Unix/macOS, PowerShell on Windows). Default timeout %ds, max 600s.", defaultTimeout),
		"- Each call starts in the workspace directory; pass `workdir` to run elsewhere instead of a leading `cd`. Shell state (env vars, aliases) does not persist between calls.",
		"- Quote paths with spaces. Chain dependent commands with `&&` (Unix, PowerShell 7+) or `; if ($?) { ... }` (Windows PowerShell 5.1); issue independent commands as separate calls.",
		"- Increase `timeout` for long-running commands; poll with a check command instead of leading sleeps.",
		"- For processes that should keep running or need interactive stdin, use `exec_command` instead.",
		"- Not for file content work: use `read`/`edit`/`write`/`grep`/`glob` instead of cat, head, tail, sed, awk, echo-redirects, or find. Files viewed through shell do not count as read for `edit`'s read-before-edit requirement.",
		safetyGuardLine(elicit),
	}, "\n")

	return []tool.Tool{{
		Name:        "shell",
		Description: description,
		Effect:      ClassifyEffect,
		Timeout:     15 * time.Minute,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"command":     map[string]any{"type": "string", "description": "Command to run."},
				"description": map[string]any{"type": "string", "description": "Short label (e.g. \"Run unit tests\")."},
				"workdir":     map[string]any{"type": "string", "description": "Directory to run the command in (absolute, or relative to the workspace). Defaults to the workspace root."},
				"timeout":     map[string]any{"type": "integer", "description": fmt.Sprintf("Seconds (default %d, max 600).", defaultTimeout)},
			},

			"required":             []string{"command"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return executeShell(ctx, workDir, extraWritableRoots, elicit, appr, args)
		},
	}}
}

func executeShell(ctx context.Context, workDir string, extraWritableRoots []string, elicit *tool.Elicitation, appr *Approvals, args map[string]any) (string, error) {
	command, ok := args["command"].(string)

	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := defaultTimeout
	if value, present, err := tool.OptionalIntArg(args, "timeout"); present {
		if err != nil {
			return "", err
		}
		timeout = value
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	if timeout > 600 {
		timeout = 600
	}

	dir, err := resolveWorkdir(workDir, args)
	if err != nil {
		return "", err
	}

	if err := confirmDangerous(ctx, elicit, appr, args, approvalWorkdir(workDir, dir)); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeoutCause(ctx, time.Duration(timeout)*time.Second, errCommandTimeout)
	defer cancel()

	cmd, err := buildCommand(ctx, command, dir, sandboxOptions{WorkspaceDir: workDir, ExtraWritableRoots: extraWritableRoots})
	if err != nil {
		return "", err
	}

	output := &progressBuffer{report: tool.Progress(ctx)}
	cmd.Stdout = output
	cmd.Stderr = output

	started := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(started)

	return formatShellResult(ctx, timeout, sanitizeOutput(output.result()), runErr, elapsed), nil
}

// formatShellResult turns a command's outcome into the text returned to the
// model: a timeout notice, an exit-code/failure notice, or the plain output
// on success.
func formatShellResult(ctx context.Context, timeout int, result string, runErr error, elapsed time.Duration) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		notice := fmt.Sprintf("Command timed out after %d seconds", timeout)
		if !errors.Is(context.Cause(ctx), errCommandTimeout) {
			notice = "Command aborted: the tool call deadline expired before the command finished"
		}
		if result == "" {
			return notice
		}
		return result + "\n\n" + notice
	}

	if runErr != nil {
		notice := ""
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			notice = fmt.Sprintf("Command exited with code %d%s", exitErr.ExitCode(), wallTimeNote(elapsed))
		} else {
			notice = fmt.Sprintf("Command failed to run: %v", runErr)
		}
		if result == "" {
			return notice
		}
		return result + "\n\n" + notice
	}

	if result == "" {
		return fmt.Sprintf("(command completed with no output%s)", wallTimeNote(elapsed))
	}
	if note := wallTimeNote(elapsed); note != "" {
		result += fmt.Sprintf("\n\n(completed%s)", note)
	}
	return result
}

// wallTimeNote reports the runtime for slow commands only — it helps the
// model calibrate timeouts and spot near-hangs without taxing the common
// fast case.
func wallTimeNote(elapsed time.Duration) string {
	if elapsed < 10*time.Second {
		return ""
	}
	return fmt.Sprintf(" after %.0fs", elapsed.Seconds())
}

// approvalWorkdir returns the directory to surface in approval prompts: empty
// for the workspace default, the effective directory otherwise.
func approvalWorkdir(workDir, dir string) string {
	if dir == workDir {
		return ""
	}
	return dir
}

func resolveWorkdir(workDir string, args map[string]any) (string, error) {
	value, _ := args["workdir"].(string)
	value = strings.TrimSpace(value)

	if value == "" {
		return workDir, nil
	}

	if !filepath.IsAbs(value) {
		value = filepath.Join(workDir, value)
	}

	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("workdir %q is not an accessible directory", value)
	}
	if workspaceSandboxEnabled() && !pathWithin(workDir, value) {
		return "", fmt.Errorf("workdir %q is outside sandbox workspace %q", value, workDir)
	}

	return value, nil
}

// Command builds an *exec.Cmd that runs a script with the same
// interpreter the shell tool uses on this platform.
func Command(ctx context.Context, command, workingDir string) (*exec.Cmd, error) {
	return buildCommand(ctx, command, workingDir, sandboxOptions{WorkspaceDir: workingDir})
}

// sandboxOptions configures how buildCommand runs a command relative to the
// workspace sandbox, kept as a struct instead of positional parameters since
// callers (the shell tool, its escalation retry, exec_command) each need a
// different subset.
type sandboxOptions struct {
	// WorkspaceDir is the sandbox boundary commands may write within.
	WorkspaceDir string
	// ExtraWritableRoots grants additional writable directories outside the
	// workspace, e.g. the project memory directory.
	ExtraWritableRoots []string
}

func buildCommand(ctx context.Context, command, workingDir string, opts sandboxOptions) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	sandboxed := false

	if runtime.GOOS == "windows" {
		ps := findPowerShell()

		wrapped := "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; " + command
		if workspaceSandboxEnabled() {
			return nil, fmt.Errorf("workspace shell sandbox is not supported on Windows")
		}
		cmd = exec.CommandContext(ctx, ps, "-NoProfile", "-NoLogo", "-NonInteractive", "-Command", wrapped)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		if workspaceSandboxEnabled() {
			var err error
			cmd, err = buildSandboxedCommand(ctx, shell, command, workingDir, opts)
			if err != nil {
				return nil, err
			}
			sandboxed = true
		} else {
			cmd = exec.CommandContext(ctx, shell, "-c", command)
		}
	}

	cmd.Dir = workingDir
	cmd.Env = append(os.Environ(),
		"GIT_EDITOR=true",
		"WINGMAN=1",
	)
	if sandboxed {
		cmd.Env = append(cmd.Env, workspaceSandboxActiveEnv+"=1")
	}

	setupProcessGroup(cmd)

	cmd.Cancel = func() error { return killProcessGroup(cmd) }

	return cmd, nil
}

func findPowerShell() string {
	if ps, err := exec.LookPath("pwsh"); err == nil {
		return ps
	}
	return "powershell"
}
