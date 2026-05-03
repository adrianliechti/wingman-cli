package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const (
	defaultTimeout = 120
	maxLines       = 2000
	maxBytes       = 50 * 1024
)

func Tools(workDir string, elicit *tool.Elicitation) []tool.Tool {
	description := strings.Join([]string{
		fmt.Sprintf("Execute a shell command and return its output. Default timeout: %ds, max: 600s.", defaultTimeout),
		"",
		"IMPORTANT: Prefer dedicated tools for routine file operations:",
		"- File search: `find` (NOT shell find/ls)",
		"- Content search: `grep` (NOT shell grep/rg)",
		"- Read files: `read` (NOT cat/head/tail)",
		"- Edit files: `edit` (NOT sed/awk)",
		"- Write files: `write` (NOT echo/cat with heredoc)",
		"Use shell for build, test, run, package-manager, git, formatter, generator, and project commands.",
		"",
		"Usage:",
		"- Always provide a brief description of what the command does.",
		"- For multiple independent commands, make multiple shell calls in parallel.",
		"- For dependent commands, chain with && in a single call (on Windows PowerShell 5.1, use `; if ($?) {` instead since && is not supported).",
		"- Use ; only when you need sequential execution but don't care if earlier commands fail.",
		"- For git: prefer new commits over amending; never use --no-verify, --force, or -i (interactive) unless explicitly asked.",
		"- If a command is long-running, increase the timeout instead of using sleep.",
	}, "\n")

	return []tool.Tool{{
		Name:        "shell",
		Description: description,
		Effect:      ClassifyEffect,

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

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return executeShell(ctx, workDir, elicit, args)
		},
	}}
}

func executeShell(ctx context.Context, workDir string, elicit *tool.Elicitation, args map[string]any) (string, error) {
	command, ok := args["command"].(string)

	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := defaultTimeout

	if t, ok := args["timeout"].(float64); ok {
		timeout = int(t)
	}

	if timeout > 600 {
		timeout = 600
	}

	if elicit != nil && elicit.Confirm != nil && ClassifyEffect(args) == tool.EffectDangerous {
		approved, err := elicit.Confirm(ctx, "❯ "+command)

		if err != nil {
			return "", fmt.Errorf("failed to get user approval: %w", err)
		}

		if !approved {
			return "", fmt.Errorf("command execution denied by user")
		}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := buildCommand(ctx, command, workDir)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd)
		return "", fmt.Errorf("command timed out after %d seconds", timeout)
	case err := <-done:
		truncated := truncateOutput(output.String())

		if err != nil {
			exitCode := -1
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			truncated += fmt.Sprintf("\n\nCommand exited with code %d", exitCode)
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

// findPowerShell prefers pwsh (PowerShell 7+, supports && and ||) and falls
// back to powershell (5.1).
func findPowerShell() string {
	if ps, err := exec.LookPath("pwsh"); err == nil {
		return ps
	}
	return "powershell"
}

func truncateOutput(output string) string {
	totalLines := strings.Count(output, "\n") + 1
	totalBytes := len(output)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return output
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

	notice := fmt.Sprintf("[Output truncated: showing last %d of %d lines (%d of %d bytes)]\n\n",
		shownLines, totalLines, shownBytes, totalBytes)

	return notice + truncated
}
