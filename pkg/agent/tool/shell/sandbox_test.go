package shell

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolveWorkdirWorkspaceSandbox(t *testing.T) {
	t.Setenv("WINGMAN_SANDBOX", "workspace")
	t.Setenv(workspaceSandboxActiveEnv, "")

	workspace := t.TempDir()
	inside := filepath.Join(workspace, "inside")
	if err := os.Mkdir(inside, 0755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	if got, err := resolveWorkdir(workspace, map[string]any{"workdir": inside}); err != nil || got != inside {
		t.Fatalf("inside workdir = %q, %v", got, err)
	}
	if _, err := resolveWorkdir(workspace, map[string]any{"workdir": outside}); err == nil || !strings.Contains(err.Error(), "outside sandbox workspace") {
		t.Fatalf("outside workdir error = %v", err)
	}
}

func TestResolveWorkdirWorkspaceSandboxRejectsSymlinkEscape(t *testing.T) {
	t.Setenv("WINGMAN_SANDBOX", "workspace")
	t.Setenv(workspaceSandboxActiveEnv, "")

	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := resolveWorkdir(workspace, map[string]any{"workdir": link}); err == nil || !strings.Contains(err.Error(), "outside sandbox workspace") {
		t.Fatalf("symlink workdir error = %v", err)
	}
}

func TestWorkspaceSandboxIsExplicitOptIn(t *testing.T) {
	t.Setenv(workspaceSandboxActiveEnv, "")
	for _, value := range []string{"", "off", "false", "1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("WINGMAN_SANDBOX", value)
			if workspaceSandboxEnabled() {
				t.Fatalf("sandbox enabled for %q", value)
			}
		})
	}
	for _, value := range []string{"workspace", "workspace-write", "WORKSPACE"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("WINGMAN_SANDBOX", value)
			if !workspaceSandboxEnabled() {
				t.Fatalf("sandbox disabled for %q", value)
			}
		})
	}
}

func TestWorkspaceSandboxDoesNotNest(t *testing.T) {
	t.Setenv("WINGMAN_SANDBOX", "workspace")
	t.Setenv(workspaceSandboxActiveEnv, "1")
	if workspaceSandboxEnabled() {
		t.Fatal("sandbox should be inherited instead of nested")
	}
}

func TestWritableRootsForIncludesValidExtraRoots(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	roots := writableRootsFor(workspace, []string{extra, missing, "", extra})

	if !slices.Contains(roots, canonicalPath(workspace)) {
		t.Fatalf("roots missing workspace: %q", roots)
	}
	if !slices.Contains(roots, canonicalPath(extra)) {
		t.Fatalf("roots missing valid extra root: %q", roots)
	}
	if slices.Contains(roots, canonicalPath(missing)) {
		t.Fatalf("roots should drop a nonexistent extra root: %q", roots)
	}
	count := 0
	for _, r := range roots {
		if r == canonicalPath(extra) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("extra root should be de-duplicated, got %d entries: %q", count, roots)
	}
}

func TestBuildSandboxedCommandRejectsMissingWorkspace(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := buildSandboxedCommand(t.Context(), "/bin/sh", "true", missing, sandboxOptions{WorkspaceDir: missing}); err == nil || !strings.Contains(err.Error(), "not an accessible directory") {
		t.Fatalf("missing workspace error = %v", err)
	}
}

func TestIsLikelySandboxDenied(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		output   string
		want     bool
	}{
		{"success", 0, "operation not permitted", false},
		{"no output keyword, ordinary failure", 1, "file not found", false},
		{"operation not permitted", 1, "touch: /outside: Operation not permitted", true},
		{"permission denied", 1, "sh: cannot create foo: Permission denied", true},
		{"read-only file system", 1, "cp: /outside/x: Read-only file system", true},
		{"sandbox keyword", 1, "sandbox_apply: Operation not permitted", true},
		{"case insensitive", 1, "PERMISSION DENIED", true},
		{"quick-reject exit code 2", 2, "operation not permitted", false},
		{"quick-reject exit code 126", 126, "permission denied", false},
		{"quick-reject exit code 127", 127, "command not found: permission denied", false},
		{"negative exit code (signal) with keyword", -1, "operation not permitted", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLikelySandboxDenied(tt.exitCode, tt.output); got != tt.want {
				t.Fatalf("isLikelySandboxDenied(%d, %q) = %v, want %v", tt.exitCode, tt.output, got, tt.want)
			}
		})
	}
}
