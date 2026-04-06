package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const (
	defaultTimeout = 120
	maxLines       = 2000
	maxBytes       = 50 * 1024
)

func ShellTool() tool.Tool {
	description := strings.Join([]string{
		fmt.Sprintf("Execute a shell command and return its output. Default timeout: %ds, max: 600s.", defaultTimeout),
		"",
		"IMPORTANT: Do NOT use this for file operations — use the dedicated tools instead:",
		"- File search: `find` (NOT shell find/ls)",
		"- Content search: `grep` (NOT shell grep/rg)",
		"- Read files: `read` (NOT cat/head/tail)",
		"- Edit files: `edit` (NOT sed/awk)",
		"- Write files: `write` (NOT echo/cat with heredoc)",
		"",
		"Usage:",
		"- Always provide a brief description of what the command does.",
		"- For multiple independent commands, make multiple shell calls in parallel.",
		"- For dependent commands, chain with && in a single call (on Windows PowerShell 5.1, use `; if ($?) {` instead since && is not supported).",
		"- Use ; only when you need sequential execution but don't care if earlier commands fail.",
		"- For git: prefer new commits over amending; never use --no-verify, --force, or -i (interactive) unless explicitly asked.",
		"- If a command is long-running, increase the timeout instead of using sleep.",
	}, "\n")

	return tool.Tool{
		Name:        "shell",
		Description: description,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},

				"description": map[string]any{
					"type":        "string",
					"description": "Brief description of what this command does (e.g., \"Run unit tests\", \"Install dependencies\")",
				},

				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Timeout in seconds (default: %d, max: 600)", defaultTimeout),
				},
			},

			"required": []string{"command"},
		},

		Execute: executeShell,
	}
}

// isSafeCommand checks if the entire command (including pipes, chains, and subshells) is safe.
func isSafeCommand(command string) bool {
	command = strings.TrimSpace(command)

	if command == "" {
		return false
	}

	// Split on pipes, chains, and semicolons — every segment must be safe
	segments := splitCommandSegments(command)

	for _, seg := range segments {
		if !isSingleCommandSafe(seg) {
			return false
		}
	}

	// Check for command substitution $(...) or `...` — treat as unsafe
	if strings.Contains(command, "$(") || strings.Contains(command, "`") {
		return false
	}

	return true
}

// splitCommandSegments splits a command string on |, &&, ||, and ; boundaries.
// It respects quoted strings and parentheses.
func splitCommandSegments(command string) []string {
	var segments []string
	var current strings.Builder

	inSingle := false
	inDouble := false
	i := 0

	for i < len(command) {
		ch := command[i]

		// Handle escape characters
		if ch == '\\' && i+1 < len(command) && !inSingle {
			current.WriteByte(ch)
			i++
			current.WriteByte(command[i])
			i++
			continue
		}

		// Handle quotes
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteByte(ch)
			i++
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteByte(ch)
			i++
			continue
		}

		// Don't split inside quotes
		if inSingle || inDouble {
			current.WriteByte(ch)
			i++
			continue
		}

		// Check for && or ||
		if i+1 < len(command) && ((ch == '&' && command[i+1] == '&') || (ch == '|' && command[i+1] == '|')) {
			seg := strings.TrimSpace(current.String())
			if seg != "" {
				segments = append(segments, seg)
			}
			current.Reset()
			i += 2
			continue
		}

		// Check for pipe |
		if ch == '|' {
			seg := strings.TrimSpace(current.String())
			if seg != "" {
				segments = append(segments, seg)
			}
			current.Reset()
			i++
			continue
		}

		// Check for semicolon ;
		if ch == ';' {
			seg := strings.TrimSpace(current.String())
			if seg != "" {
				segments = append(segments, seg)
			}
			current.Reset()
			i++
			continue
		}

		current.WriteByte(ch)
		i++
	}

	seg := strings.TrimSpace(current.String())
	if seg != "" {
		segments = append(segments, seg)
	}

	return segments
}

// isSingleCommandSafe checks if a single command (no pipes/chains) is safe.
func isSingleCommandSafe(command string) bool {
	command = strings.TrimSpace(command)

	// Extract words from the command
	words := strings.Fields(command)

	if len(words) == 0 {
		return false
	}

	cmd := strings.ToLower(filepath.Base(words[0]))
	if _, ok := safeCommandSet[cmd]; ok {
		return true
	}

	allowedSubcmds, hasSubcmds := safeSubcommandPrefixes[cmd]

	if !hasSubcmds {
		return false
	}

	if len(words) < 2 {
		return false
	}

	restOfCommand := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(command, words[0])))

	for _, subCmd := range allowedSubcmds {
		if strings.HasPrefix(restOfCommand, subCmd) {
			return true
		}
	}

	return false
}

func executeShell(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
	command, ok := args["command"].(string)

	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}

	if env != nil && env.IsPlanning() && !isSafeCommand(command) {
		return "", fmt.Errorf("plan mode only allows read-only shell commands")
	}

	timeout := defaultTimeout

	if t, ok := args["timeout"].(float64); ok {
		timeout = int(t)
	}

	// Cap timeout at 600 seconds
	if timeout > 600 {
		timeout = 600
	}

	if env.PromptUser != nil && !isSafeCommand(command) {
		approved, err := env.PromptUser("❯ " + command)

		if err != nil {
			return "", fmt.Errorf("failed to get user approval: %w", err)
		}

		if !approved {
			return "", fmt.Errorf("command execution denied by user")
		}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := buildCommand(ctx, command, env.RootDir())

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Start()

	if err != nil {
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	done := make(chan error, 1)

	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd)

		return "", fmt.Errorf("command timed out after %d seconds", timeout)
	case err := <-done:
		truncated := truncateOutput(output.String(), env.ScratchDir())

		if err != nil {
			exitCode := -1

			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}

			truncated += fmt.Sprintf("\n\nCommand exited with code %d", exitCode)

			return truncated, nil
		}

		return truncated, nil
	}
}

func buildCommand(ctx context.Context, command, workingDir string) *exec.Cmd {
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		ps := findPowerShell()
		// Force UTF-8 output to avoid PowerShell 5.1's UTF-16 default
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
	cmd.Env = append(os.Environ(),
		"GIT_EDITOR=true", // Prevent git from opening interactive editors
		"WINGMAN=1",       // Marker so scripts can detect agent context
	)

	setupProcessGroup(cmd)

	return cmd
}

// findPowerShell returns the path to PowerShell. Prefers pwsh (PowerShell 7+)
// which supports && and || operators. Falls back to powershell (5.1).
func findPowerShell() string {
	if ps, err := exec.LookPath("pwsh"); err == nil {
		return ps
	}
	return "powershell"
}

func truncateOutput(output string, sessionDir string) string {
	totalLines := strings.Count(output, "\n") + 1
	totalBytes := len(output)

	needsTruncation := totalLines > maxLines || totalBytes > maxBytes

	if !needsTruncation {
		return output
	}

	var tempFile string

	if sessionDir != "" {
		tempFile = filepath.Join(sessionDir, fmt.Sprintf("output-%d.txt", time.Now().UnixNano()))
		os.WriteFile(tempFile, []byte(output), 0644)
	}

	lines := strings.Split(output, "\n")

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	truncated := strings.Join(lines, "\n")

	if len(truncated) > maxBytes {
		truncated = truncated[len(truncated)-maxBytes:]
	}

	shownLines := strings.Count(truncated, "\n") + 1
	shownBytes := len(truncated)

	var notice string

	if tempFile != "" {
		notice = fmt.Sprintf("[Output truncated: showing last %d of %d lines (%d of %d bytes). Full output: %s]\n\n",
			shownLines, totalLines, shownBytes, totalBytes, tempFile)
	} else {
		notice = fmt.Sprintf("[Output truncated: showing last %d of %d lines (%d of %d bytes)]\n\n",
			shownLines, totalLines, shownBytes, totalBytes)
	}

	return notice + truncated
}
