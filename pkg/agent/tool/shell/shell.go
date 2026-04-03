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

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const (
	defaultTimeout = 120
	maxLines       = 2000
	maxBytes       = 50 * 1024
)

func ShellTool() tool.Tool {
	description := strings.Join([]string{
		fmt.Sprintf("Execute a shell command and return its output. Runs in the working directory with a default timeout of %d seconds.", defaultTimeout),
		"",
		"IMPORTANT: Avoid using this tool to run find, grep, cat, head, tail, sed, or awk commands. Use the dedicated tools instead:",
		"- File search: Use `find` (NOT shell find or ls)",
		"- Content search: Use `grep` (NOT shell grep or rg)",
		"- Read files: Use `read` (NOT cat/head/tail)",
		"- Edit files: Use `edit` (NOT sed/awk)",
		"- Write files: Use `write` (NOT echo/cat with heredoc)",
		"",
		"Usage:",
		"- When issuing multiple independent commands, make multiple shell tool calls in parallel.",
		"- For dependent commands, chain with && in a single call.",
		"- For git commands: prefer new commits over amending; never use --no-verify or --force unless explicitly asked; never use -i (interactive) flags.",
		"- Avoid unnecessary sleep commands. If a command is long-running, increase the timeout instead.",
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

// isSafeCommand checks if the command starts with a known safe/read-only command
func isSafeCommand(command string) bool {
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

func executeShell(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
	command, ok := args["command"].(string)

	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := defaultTimeout

	if t, ok := args["timeout"].(float64); ok {
		timeout = int(t)
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

	cmd := buildCommand(ctx, command, env.WorkingDir())

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
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NoLogo", "-NonInteractive", "-Command", command)
		cmd.Dir = workingDir

		setupProcessGroup(cmd)

		return cmd
	}

	shell := os.Getenv("SHELL")

	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = workingDir

	setupProcessGroup(cmd)

	return cmd
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
