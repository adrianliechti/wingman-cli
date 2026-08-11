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
	defaultTimeout       = 120
	maxOutputBytes       = 16 * 1024 * 1024
	maxSpillBytes        = 256 * 1024 * 1024
	outputHeadBytes      = 4 * 1024
	outputTailBytes      = 8 * 1024
	outputTranscriptMode = 0600
)

var errCommandTimeout = errors.New("command timeout")

type cappedBuffer struct {
	buf        bytes.Buffer
	head       []byte
	tail       []byte
	total      int64
	dropped    int64
	overflow   bool
	scratchDir string
	spillLimit int64
	spill      *os.File
	spillPath  string
	spillSize  int64
}

func newCappedBuffer(scratchDir string) *cappedBuffer {
	return &cappedBuffer{scratchDir: scratchDir}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.total += int64(len(p))
	if len(b.head) < outputHeadBytes {
		n := min(len(p), outputHeadBytes-len(b.head))
		b.head = append(b.head, p[:n]...)
	}
	b.tail = append(b.tail, p...)
	if over := len(b.tail) - outputTailBytes; over > 0 {
		copy(b.tail, b.tail[over:])
		b.tail = b.tail[:outputTailBytes]
	}

	if !b.overflow {
		if b.buf.Len()+len(p) <= maxOutputBytes {
			_, _ = b.buf.Write(p)
			return len(p), nil
		}
		b.overflow = true
		b.startSpill()
		if b.spill != nil {
			b.writeSpill(b.buf.Bytes())
		}
		if b.spill != nil {
			// The inline buffer is freed only once its bytes are on disk;
			// without a transcript it stays as the model-visible output.
			b.buf = bytes.Buffer{}
		}
	}

	if b.spill != nil {
		b.writeSpill(p)
	} else {
		b.dropped += int64(len(p))
	}
	return len(p), nil
}

func (b *cappedBuffer) startSpill() {
	if b.scratchDir == "" {
		return
	}
	f, err := os.CreateTemp(b.scratchDir, "shell-output-*.log")
	if err != nil {
		return
	}
	_ = f.Chmod(outputTranscriptMode)
	b.spill = f
	b.spillPath = f.Name()
}

func (b *cappedBuffer) writeSpill(p []byte) {
	limit := b.spillLimit
	if limit == 0 {
		limit = maxSpillBytes
	}
	if room := limit - b.spillSize; int64(len(p)) > room {
		keep := max(room, 0)
		b.dropped += int64(len(p)) - keep
		p = p[:keep]
	}
	if len(p) == 0 {
		return
	}
	n, err := b.spill.Write(p)
	b.spillSize += int64(n)
	if err != nil || n != len(p) {
		_ = b.spill.Close()
		_ = os.Remove(b.spillPath)
		b.spill = nil
		b.spillPath = ""
	}
}

func (b *cappedBuffer) result() string {
	if b.spill != nil {
		_ = b.spill.Close()
		b.spill = nil
	}

	if !b.overflow {
		return b.buf.String()
	}

	var out strings.Builder

	if b.buf.Len() > 0 {
		out.Write(b.buf.Bytes())
		fmt.Fprintf(&out, "\n\n[output exceeded %dMB; %d trailing bytes dropped (no scratch directory for a full transcript)]", maxOutputBytes/(1024*1024), b.dropped)
		return out.String()
	}

	out.Write(b.head)
	out.WriteString("\n\n[... middle output omitted ...]\n\n")
	out.Write(b.tail)
	middleBytes := b.total - int64(len(b.head)) - int64(len(b.tail))
	fmt.Fprintf(&out, "\n\n[output exceeded %dMB; %d middle bytes omitted from inline output", maxOutputBytes/(1024*1024), middleBytes)
	switch {
	case b.spillPath != "" && b.dropped > 0:
		fmt.Fprintf(&out, "; first %d bytes of raw output saved to %s", b.spillSize, b.spillPath)
	case b.spillPath != "":
		fmt.Fprintf(&out, "; full raw output saved to %s", b.spillPath)
	}
	out.WriteString("]")
	return out.String()
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
	*cappedBuffer
	report func(string)

	partial []byte
	lastAt  time.Time
}

func (b *progressBuffer) Write(p []byte) (int, error) {
	if b.cappedBuffer == nil {
		b.cappedBuffer = &cappedBuffer{}
	}
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

func Tools(workDir string, elicit *tool.Elicitation, appr *Approvals, opts *Options) []tool.Tool {
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

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			return executeShell(ctx, workDir, elicit, appr, opts, args)
		},
	}}
}

func executeShell(ctx context.Context, workDir string, elicit *tool.Elicitation, appr *Approvals, opts *Options, args map[string]any) (tool.Result, error) {
	command, ok := args["command"].(string)

	if !ok || command == "" {
		return tool.Result{}, fmt.Errorf("command is required")
	}

	timeout := defaultTimeout
	if value, present, err := tool.OptionalIntArg(args, "timeout"); present {
		if err != nil {
			return tool.Result{}, err
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
		return tool.Result{}, err
	}

	if err := confirmDangerous(ctx, elicit, appr, args, approvalWorkdir(workDir, dir)); err != nil {
		return tool.Result{}, err
	}

	ctx, cancel := context.WithTimeoutCause(ctx, time.Duration(timeout)*time.Second, errCommandTimeout)
	defer cancel()

	cmd := buildToolCommand(ctx, command, dir, opts)

	scratchDir := ""
	if opts != nil {
		scratchDir = opts.ScratchDir
	}
	output := &progressBuffer{cappedBuffer: newCappedBuffer(scratchDir), report: tool.Progress(ctx)}
	cmd.Stdout = output
	cmd.Stderr = output

	started := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(started)
	result := sanitizeOutput(output.result())
	metadata := map[string]any{
		"duration_ms": elapsed.Milliseconds(),
	}
	if output.overflow {
		metadata["truncated"] = true
		metadata["output_bytes"] = output.total
		if output.spillPath != "" {
			metadata["artifact_path"] = output.spillPath
		}
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		metadata["timed_out"] = true
		notice := fmt.Sprintf("Command timed out after %d seconds", timeout)
		if !errors.Is(context.Cause(ctx), errCommandTimeout) {
			notice = "Command aborted: the tool call deadline expired before the command finished"
		}
		if result == "" {
			return tool.Result{Content: notice, IsError: true, Metadata: metadata}, nil
		}
		return tool.Result{Content: result + "\n\n" + notice, IsError: true, Metadata: metadata}, nil
	}

	if runErr != nil {
		notice := ""
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			metadata["exit_code"] = exitErr.ExitCode()
			notice = fmt.Sprintf("Command exited with code %d%s", exitErr.ExitCode(), wallTimeNote(elapsed))
		} else {
			notice = fmt.Sprintf("Command failed to run: %v", runErr)
		}
		if result == "" {
			result = notice
		} else {
			result += "\n\n" + notice
		}
		return tool.Result{Content: result, IsError: true, Metadata: metadata}, nil
	}

	metadata["exit_code"] = 0
	if result == "" {
		return tool.Result{Content: fmt.Sprintf("(command completed with no output%s)", wallTimeNote(elapsed)), Metadata: metadata}, nil
	}
	if note := wallTimeNote(elapsed); note != "" {
		result += fmt.Sprintf("\n\n(completed%s)", note)
	}
	return tool.Result{Content: result, Metadata: metadata}, nil
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

	return value, nil
}

// Command builds an *exec.Cmd that runs a script with the same
// interpreter the shell tool uses on this platform.
func Command(ctx context.Context, command, workingDir string) *exec.Cmd {
	return buildCommand(ctx, command, workingDir)
}

func buildCommand(ctx context.Context, command, workingDir string) *exec.Cmd {
	return buildCommandWithEnvironment(ctx, command, workingDir, os.Environ())
}

func buildToolCommand(ctx context.Context, command, workingDir string, opts *Options) *exec.Cmd {
	return buildCommandWithEnvironment(ctx, command, workingDir, environmentForTools(opts))
}

func buildCommandWithEnvironment(ctx context.Context, command, workingDir string, environment []string) *exec.Cmd {
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		ps := findPowerShell()

		wrapped := "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; " + command
		cmd = exec.CommandContext(ctx, ps, "-NoProfile", "-NoLogo", "-NonInteractive", "-Command", wrapped)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.CommandContext(ctx, shell, "-c", command)
	}

	cmd.Dir = workingDir
	cmd.Env = setEnvironment(environment, "GIT_EDITOR", "true")
	cmd.Env = setEnvironment(cmd.Env, "WINGMAN", "1")

	setupProcessGroup(cmd)

	cmd.Cancel = func() error { return killProcessGroup(cmd) }

	return cmd
}

func findPowerShell() string {
	if ps, err := exec.LookPath("pwsh"); err == nil {
		return ps
	}
	return "powershell"
}
