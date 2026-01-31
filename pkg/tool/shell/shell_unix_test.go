//go:build !windows

package shell

import (
	"context"
	"os"
	"testing"
)

func TestBuildCommandUnix_PreservesCommandString(t *testing.T) {
	ctx := context.Background()
	workingDir := "/tmp"
	command := `printf "%s" 'a b' && echo "c\"d" && echo \"$HOME\"`

	oldShell := os.Getenv("SHELL")
	if err := os.Setenv("SHELL", "/bin/sh"); err != nil {
		t.Fatalf("failed to set SHELL: %v", err)
	}
	defer func() {
		_ = os.Setenv("SHELL", oldShell)
	}()

	cmd := buildCommand(ctx, command, workingDir)
	if cmd.Path != "/bin/sh" {
		t.Fatalf("expected shell path /bin/sh, got %q", cmd.Path)
	}
	if len(cmd.Args) < 3 {
		t.Fatalf("expected at least 3 args, got %d", len(cmd.Args))
	}
	if cmd.Args[1] != "-c" {
		t.Fatalf("expected -c flag, got %q", cmd.Args[1])
	}
	if cmd.Args[2] != command {
		t.Fatalf("command string was modified: got %q", cmd.Args[2])
	}
	if cmd.Dir != workingDir {
		t.Fatalf("expected working dir %q, got %q", workingDir, cmd.Dir)
	}
}




































