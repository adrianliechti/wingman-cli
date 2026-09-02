package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func safetyGuardLine(elicit *tool.Elicitation) string {
	if elicit == nil || elicit.Confirm == nil {
		return "- There is NO confirmation gate: commands run immediately. Never run destructive or privilege-escalating commands (recursive deletes, sudo, force-push) unless the user explicitly asked for that exact action."
	}
	return "- Safety guard: routine mutating commands run directly, but destructive or privilege-escalating commands require user confirmation first. An approved command re-runs without re-asking for the rest of the session."
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

// Command builds an *exec.Cmd that runs a script with the same interpreter
// used by exec_command on this platform.
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
