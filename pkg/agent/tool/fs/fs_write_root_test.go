package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func createWriteRootSetup(t *testing.T) (*os.Root, string, string, func()) {
	t.Helper()

	parent, err := os.MkdirTemp("", "fs_write_root_*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}

	workspaceDir := filepath.Join(parent, "workspace")
	allowedDir := filepath.Join(parent, "memory")
	for _, d := range []string{workspaceDir, allowedDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			os.RemoveAll(parent)
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	root, err := os.OpenRoot(workspaceDir)
	if err != nil {
		os.RemoveAll(parent)
		t.Fatalf("open root: %v", err)
	}

	cleanup := func() {
		root.Close()
		os.RemoveAll(parent)
	}
	return root, workspaceDir, allowedDir, cleanup
}

func TestWriteToolAllowsWritesInsideAllowedRoot(t *testing.T) {
	root, _, allowedDir, cleanup := createWriteRootSetup(t)
	defer cleanup()

	writeTool := WriteTool(root, allowedDir)

	target := filepath.Join(allowedDir, "feedback_testing.md")
	if _, err := writeTool.Execute(context.Background(), map[string]any{
		"file_path": target,
		"content":   "hello memory",
	}); err != nil {
		t.Fatalf("write inside allowed root: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "hello memory" {
		t.Errorf("unexpected content: %q", data)
	}
}

func TestWriteToolRejectsWritesOutsideAllowedRoots(t *testing.T) {
	root, _, allowedDir, cleanup := createWriteRootSetup(t)
	defer cleanup()

	writeTool := WriteTool(root, allowedDir)

	other, err := os.MkdirTemp("", "fs_other_*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(other)

	_, err = writeTool.Execute(context.Background(), map[string]any{
		"file_path": filepath.Join(other, "x.md"),
		"content":   "nope",
	})
	if err == nil {
		t.Fatal("expected error writing outside workspace and allowed roots")
	}
	if !strings.Contains(err.Error(), "outside workspace") {
		t.Errorf("expected sandbox error, got: %v", err)
	}
}

func TestEditToolWorksInsideAllowedRoot(t *testing.T) {
	root, _, allowedDir, cleanup := createWriteRootSetup(t)
	defer cleanup()

	target := filepath.Join(allowedDir, "MEMORY.md")
	if err := os.WriteFile(target, []byte("- old line\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	editTool := EditTool(root, allowedDir)
	if _, err := editTool.Execute(context.Background(), map[string]any{
		"file_path":  target,
		"old_string": "old line",
		"new_string": "new line",
	}); err != nil {
		t.Fatalf("edit inside allowed root: %v", err)
	}

	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), "new line") {
		t.Errorf("edit did not apply: %s", data)
	}
}

func TestEditToolRejectsEditsOutsideAllowedRoots(t *testing.T) {
	root, _, allowedDir, cleanup := createWriteRootSetup(t)
	defer cleanup()

	editTool := EditTool(root, allowedDir)

	other, err := os.MkdirTemp("", "fs_other_*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(other)

	target := filepath.Join(other, "x.md")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err = editTool.Execute(context.Background(), map[string]any{
		"file_path":  target,
		"old_string": "seed",
		"new_string": "tampered",
	})
	if err == nil {
		t.Fatal("expected error editing outside workspace and allowed roots")
	}
}

func TestExpandHomeAcrossTools(t *testing.T) {

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	allowedDir, err := os.MkdirTemp(home, ".wingman_test_home_*")
	if err != nil {
		t.Skipf("cannot mkdtemp under home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(allowedDir) })

	rel, err := filepath.Rel(home, allowedDir)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	tildePrefix := "~/" + filepath.ToSlash(rel)

	workspaceDir, err := os.MkdirTemp("", "fs_home_workspace_*")
	if err != nil {
		t.Fatalf("mkdtemp workspace: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(workspaceDir) })

	root, err := os.OpenRoot(workspaceDir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	t.Cleanup(func() { root.Close() })

	t.Run("write expands ~/", func(t *testing.T) {
		_, err := WriteTool(root, allowedDir).Execute(context.Background(), map[string]any{
			"file_path": tildePrefix + "/note.md",
			"content":   "via tilde",
		})
		if err != nil {
			t.Fatalf("write via ~/: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(allowedDir, "note.md"))
		if string(data) != "via tilde" {
			t.Errorf("expected file written via ~/, got %q", data)
		}
	})

	t.Run("edit expands ~/", func(t *testing.T) {
		_, err := EditTool(root, allowedDir).Execute(context.Background(), map[string]any{
			"file_path":  tildePrefix + "/note.md",
			"old_string": "via tilde",
			"new_string": "via tilde edited",
		})
		if err != nil {
			t.Fatalf("edit via ~/: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(allowedDir, "note.md"))
		if string(data) != "via tilde edited" {
			t.Errorf("expected edit via ~/, got %q", data)
		}
	})

	t.Run("glob expands ~/", func(t *testing.T) {
		result, err := GlobTool(root, allowedDir).Execute(context.Background(), map[string]any{
			"pattern": "*.md",
			"path":    tildePrefix,
		})
		if err != nil {
			t.Fatalf("glob via ~/: %v", err)
		}
		if !strings.Contains(result.Content, "note.md") {
			t.Errorf("expected glob to find note.md via ~/, got: %s", result.Content)
		}
	})

	t.Run("grep expands ~/", func(t *testing.T) {
		result, err := GrepTool(root, allowedDir).Execute(context.Background(), map[string]any{
			"pattern": "via tilde",
			"path":    tildePrefix,
		})
		if err != nil {
			t.Fatalf("grep via ~/: %v", err)
		}
		if !strings.Contains(result.Content, "note.md") {
			t.Errorf("expected grep to find note.md via ~/, got: %s", result.Content)
		}
	})
}

func TestWriteToolWithoutAllowedRootsStillRespectsWorkspace(t *testing.T) {
	root, workspaceDir, _, cleanup := createWriteRootSetup(t)
	defer cleanup()

	writeTool := WriteTool(root)

	rel := "in_workspace.txt"
	if _, err := writeTool.Execute(context.Background(), map[string]any{
		"file_path": rel,
		"content":   "in workspace",
	}); err != nil {
		t.Fatalf("workspace write: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(workspaceDir, rel))
	if string(data) != "in workspace" {
		t.Errorf("workspace write did not land: %q", data)
	}

	sibling, err := os.MkdirTemp("", "fs_sib_*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(sibling)

	if _, err := writeTool.Execute(context.Background(), map[string]any{
		"file_path": filepath.Join(sibling, "x.txt"),
		"content":   "nope",
	}); err == nil {
		t.Fatal("expected sandbox rejection for sibling dir")
	}
}
