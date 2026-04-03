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

func TestIsSafeCommand_PipeSafety(t *testing.T) {
	tests := []struct {
		command string
		safe    bool
	}{
		// Simple safe commands
		{"ls", true},
		{"git status", true},
		{"cat foo.txt", true},
		{"echo hello", true},

		// Safe pipes (all segments safe)
		{"cat foo.txt | grep bar", true},
		{"git log | head -20", true},
		{"ls -la | sort | head", true},

		// Unsafe pipes (at least one segment unsafe)
		{"echo foo | rm -rf /", false},
		{"cat foo | xargs rm", false},
		{"ls | xargs chmod 777", false},

		// Unsafe chains
		{"cat foo && rm -rf /", false},
		{"echo hello ; rm -rf /", false},
		{"git status || rm -rf /", false},

		// Command substitution is always unsafe
		{"echo $(whoami)", false},
		{"echo `whoami`", false},

		// Quoted pipes should not be split
		{`echo "hello | world"`, true},
		{`echo 'hello && world'`, true},

		// Safe chained commands
		{"git status && git diff", true},
		{"ls ; echo done", true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := isSafeCommand(tt.command)
			if got != tt.safe {
				t.Errorf("isSafeCommand(%q) = %v, want %v", tt.command, got, tt.safe)
			}
		})
	}
}

func TestSplitCommandSegments(t *testing.T) {
	tests := []struct {
		command  string
		expected []string
	}{
		{"ls", []string{"ls"}},
		{"ls | grep foo", []string{"ls", "grep foo"}},
		{"echo a && echo b", []string{"echo a", "echo b"}},
		{"a || b ; c", []string{"a", "b", "c"}},
		{`echo "a | b" && echo c`, []string{`echo "a | b"`, "echo c"}},
		{`echo 'a && b'`, []string{`echo 'a && b'`}},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := splitCommandSegments(tt.command)
			if len(got) != len(tt.expected) {
				t.Fatalf("splitCommandSegments(%q) = %v, want %v", tt.command, got, tt.expected)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("segment %d: got %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}




































