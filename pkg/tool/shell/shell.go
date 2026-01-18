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

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

const (
	defaultTimeout = 120
	maxLines       = 2000
	maxBytes       = 50 * 1024
)

func ShellTool() tool.Tool {
	return tool.Tool{
		Name:        "shell",
		Description: "Execute a shell command. The command runs in the working directory. On Unix systems, uses $SHELL or /bin/sh. On Windows, uses cmd.exe. Returns stdout/stderr combined. If output is truncated, a temp file path is provided to read the full output.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Timeout in seconds (default: %d)", defaultTimeout),
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

	cmd := words[0]

	// Handle paths like /usr/bin/ls or ./script
	cmd = filepath.Base(cmd)

	// First, check if it's a simple safe command (no subcommand check needed)
	for _, safe := range safeCommands {
		if cmd == safe {
			return true
		}
	}

	// Check if this command requires subcommand validation
	if allowedSubcmds, hasSubcmds := safeSubcommands[cmd]; hasSubcmds {
		if len(words) < 2 {
			// Command requires subcommand but none provided
			return false
		}

		// Get the rest of the command after the main command
		restOfCommand := strings.TrimSpace(strings.TrimPrefix(command, words[0]))

		for _, subCmd := range allowedSubcmds {
			if strings.HasPrefix(restOfCommand, subCmd) {
				return true
			}
		}
		return false
	}

	return false
}

func executeShell(env *tool.Environment, args map[string]any) (string, error) {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := defaultTimeout
	if t, ok := args["timeout"].(float64); ok {
		timeout = int(t)
	}

	if env.PromptUser != nil && !isSafeCommand(command) {
		approved, err := env.PromptUser("$" + command)
		if err != nil {
			return "", fmt.Errorf("failed to get user approval: %w", err)
		}

		if !approved {
			return "", fmt.Errorf("command execution denied by user")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
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
		result := output.String()
		truncated, tempFile := truncateOutput(result, env.ScratchDir())

		if err != nil {
			exitCode := -1
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}

			msg := truncated
			if tempFile != "" {
				msg += fmt.Sprintf("\n\n[Output truncated. Full output saved to: %s]", tempFile)
			}
			msg += fmt.Sprintf("\n\nCommand exited with code %d", exitCode)

			return msg, nil
		}

		if tempFile != "" {
			truncated += fmt.Sprintf("\n\n[Output truncated. Full output saved to: %s]", tempFile)
		}

		return truncated, nil
	}
}

func buildCommand(ctx context.Context, command, workingDir string) *exec.Cmd {
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", command)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.CommandContext(ctx, shell, "-c", command)
	}

	cmd.Dir = workingDir
	setupProcessGroup(cmd)

	return cmd
}

func truncateOutput(output string, sessionDir string) (truncated string, tempFile string) {
	lines := strings.Split(output, "\n")
	byteCount := len(output)

	needsTruncation := len(lines) > maxLines || byteCount > maxBytes

	if !needsTruncation {
		return output, ""
	}

	if sessionDir != "" {
		tempFile = filepath.Join(sessionDir, fmt.Sprintf("output-%d.txt", time.Now().UnixNano()))
		os.WriteFile(tempFile, []byte(output), 0644)
	}

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	truncated = strings.Join(lines, "\n")

	if len(truncated) > maxBytes {
		truncated = truncated[len(truncated)-maxBytes:]
	}

	return truncated, tempFile
}
