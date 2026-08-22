package commandpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveProjectWalksToWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	name := Candidates(runtime.GOOS, "example-tool")[0]
	command := filepath.Join(root, "node_modules", ".bin", name)
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveProject(project, root, "example-tool"); got != command {
		t.Fatalf("ResolveProject = %q, want %q", got, command)
	}
}

func TestResolveProjectRejectsDirectoryOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if got := ResolveProject(outside, root, "example-tool"); got != "" {
		t.Fatalf("ResolveProject outside workspace = %q", got)
	}
}

func TestLookPathRejectsMissingAbsoluteCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	if _, err := LookPath(path); err != exec.ErrNotFound {
		t.Fatalf("LookPath error = %v, want exec.ErrNotFound", err)
	}
}
