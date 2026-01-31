//go:build windows

package shell

import (
	"context"
	"testing"
)

func TestBuildCommandWindows_PreservesCommandString(t *testing.T) {
	ctx := context.Background()
	workingDir := `C:\`
	command := `Write-Output "a b"; Write-Output 'c"d'; Write-Output "$env:Path"`

	cmd := buildCommand(ctx, command, workingDir)
	if cmd.Path != "powershell" {
		t.Fatalf("expected powershell path, got %q", cmd.Path)
	}
	if len(cmd.Args) < 6 {
		t.Fatalf("expected at least 6 args, got %d", len(cmd.Args))
	}
	if cmd.Args[1] != "-NoProfile" || cmd.Args[2] != "-NoLogo" || cmd.Args[3] != "-NonInteractive" || cmd.Args[4] != "-Command" {
		t.Fatalf("unexpected PowerShell args: %v", cmd.Args)
	}
	if cmd.Args[5] != command {
		t.Fatalf("command string was modified: got %q", cmd.Args[5])
	}
	if cmd.Dir != workingDir {
		t.Fatalf("expected working dir %q, got %q", workingDir, cmd.Dir)
	}
}
