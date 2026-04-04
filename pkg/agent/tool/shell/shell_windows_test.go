//go:build windows

package shell

import (
	"context"
	"strings"
	"testing"
)

func TestBuildCommandWindows_UsesCorrectShell(t *testing.T) {
	ctx := context.Background()
	workingDir := `C:\`
	command := `Write-Output "hello"`

	cmd := buildCommand(ctx, command, workingDir)

	// Should use either pwsh or powershell
	base := cmd.Path
	if !strings.Contains(base, "pwsh") && !strings.Contains(base, "powershell") {
		t.Fatalf("expected pwsh or powershell, got %q", base)
	}
}

func TestBuildCommandWindows_SetsCorrectFlags(t *testing.T) {
	ctx := context.Background()
	workingDir := `C:\`
	command := `Write-Output "hello"`

	cmd := buildCommand(ctx, command, workingDir)

	// Check that -NoProfile, -NoLogo, -NonInteractive, -Command are present
	args := strings.Join(cmd.Args, " ")
	for _, flag := range []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-Command"} {
		if !strings.Contains(args, flag) {
			t.Errorf("missing flag %q in args: %v", flag, cmd.Args)
		}
	}
}

func TestBuildCommandWindows_SetsUTF8Encoding(t *testing.T) {
	ctx := context.Background()
	command := `Get-Process`

	cmd := buildCommand(ctx, command, `C:\`)

	// The last arg should contain the UTF-8 encoding prefix + original command
	lastArg := cmd.Args[len(cmd.Args)-1]
	if !strings.Contains(lastArg, "[Console]::OutputEncoding") {
		t.Error("expected UTF-8 encoding wrapper in command")
	}
	if !strings.Contains(lastArg, command) {
		t.Error("expected original command to be preserved")
	}
}

func TestBuildCommandWindows_SetsWorkingDir(t *testing.T) {
	ctx := context.Background()
	workingDir := `C:\Users\test`

	cmd := buildCommand(ctx, "dir", workingDir)

	if cmd.Dir != workingDir {
		t.Fatalf("expected working dir %q, got %q", workingDir, cmd.Dir)
	}
}

func TestBuildCommandWindows_SetsGitEditor(t *testing.T) {
	ctx := context.Background()
	cmd := buildCommand(ctx, "git status", `C:\`)

	found := false
	for _, env := range cmd.Env {
		if env == "GIT_EDITOR=true" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected GIT_EDITOR=true in environment")
	}
}

func TestFindPowerShell(t *testing.T) {
	ps := findPowerShell()
	// Should return something non-empty
	if ps == "" {
		t.Fatal("findPowerShell returned empty string")
	}
	// Should be either pwsh or powershell
	if !strings.Contains(ps, "pwsh") && !strings.Contains(ps, "powershell") {
		t.Errorf("expected pwsh or powershell, got %q", ps)
	}
}
