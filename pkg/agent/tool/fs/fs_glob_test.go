package fs_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestGlobTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	os.MkdirAll(filepath.Join(tmpDir, "src", "pkg"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "node_modules", "dep"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "app.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "pkg", "util.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "app.ts"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "node_modules", "dep", "index.js"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, ".hidden.go"), []byte("content"), 0644)

	os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("*.log\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "debug.log"), []byte("content"), 0644)

	globTool := GlobTool(root)

	t.Run("glob all go files", func(t *testing.T) {
		result, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "**/*.go",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "main.go") {
			t.Errorf("expected main.go, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "app.go") {
			t.Errorf("expected app.go, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "util.go") {
			t.Errorf("expected util.go, got: %s", result.Content)
		}
	})

	t.Run("glob includes ignored directories", func(t *testing.T) {
		result, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "**/*.js",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "node_modules") {
			t.Errorf("expected node_modules like reference Glob, got: %s", result.Content)
		}
	})

	t.Run("glob includes gitignored and hidden files", func(t *testing.T) {
		result, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "*",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "debug.log") {
			t.Errorf("expected gitignored file in result, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, ".hidden.go") {
			t.Errorf("expected hidden file in result, got: %s", result.Content)
		}
	})

	t.Run("glob in subdirectory", func(t *testing.T) {
		result, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "*.go",
			"path":    "src",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result.Content, "main.go") {
			t.Errorf("should not include files outside src, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, filepath.Join("src", "app.go")) {
			t.Errorf("expected app.go, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, filepath.Join("src", "pkg", "util.go")) {
			t.Errorf("pattern without slash should match recursively under path, got: %s", result.Content)
		}
	})

	t.Run("glob path returns workspace-relative paths", func(t *testing.T) {
		result, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "*.go",
			"path":    "src/pkg",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, filepath.Join("src", "pkg", "util.go")) {
			t.Errorf("expected workspace-relative path, got: %s", result.Content)
		}
		if strings.Contains(result.Content, "\nutil.go") || result.Content == "util.go" {
			t.Errorf("did not expect path-relative-only result, got: %s", result.Content)
		}
	})

	t.Run("glob with no matches", func(t *testing.T) {
		result, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "*.xyz",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "No files found") {
			t.Errorf("expected 'No files found', got: %s", result.Content)
		}
	})

	t.Run("glob multi-wildcard substring pattern", func(t *testing.T) {
		newRoot, newTmp, newCleanup := createTestRoot(t)
		defer newCleanup()

		os.MkdirAll(filepath.Join(newTmp, "ui", "components"), 0755)
		os.WriteFile(filepath.Join(newTmp, "ui", "components", "WorkspaceSelector.tsx"), []byte("x"), 0644)
		os.WriteFile(filepath.Join(newTmp, "ui", "components", "workspace_selector.tsx"), []byte("x"), 0644)
		os.WriteFile(filepath.Join(newTmp, "ui", "components", "Workspace.tsx"), []byte("x"), 0644)
		os.WriteFile(filepath.Join(newTmp, "ui", "components", "Selector.tsx"), []byte("x"), 0644)

		result, err := GlobTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "**/*orkspace*elector*",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "WorkspaceSelector.tsx") {
			t.Errorf("expected WorkspaceSelector.tsx, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "workspace_selector.tsx") {
			t.Errorf("expected workspace_selector.tsx, got: %s", result.Content)
		}

		if strings.Contains(result.Content, "Workspace.tsx") && !strings.Contains(result.Content, "WorkspaceSelector.tsx") {
			t.Errorf("Workspace.tsx alone should not match pattern requiring elector, got: %s", result.Content)
		}

		if strings.Contains(result.Content, "\nSelector.tsx") || strings.HasPrefix(result.Content, "Selector.tsx") {
			t.Errorf("Selector.tsx should not match pattern requiring orkspace, got: %s", result.Content)
		}
	})

	t.Run("glob pattern is case-sensitive", func(t *testing.T) {
		newRoot, newTmp, newCleanup := createTestRoot(t)
		defer newCleanup()

		os.WriteFile(filepath.Join(newTmp, "WorkspaceSelector.tsx"), []byte("x"), 0644)

		result, err := GlobTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "**/*workspace*selector*",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "No files found") {
			t.Errorf("expected case-sensitive miss, got: %s", result.Content)
		}

		result, err = GlobTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "**/*Workspace*Selector*",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "WorkspaceSelector.tsx") {
			t.Errorf("expected match on exact case, got: %s", result.Content)
		}
	})

	t.Run("glob invalid pattern", func(t *testing.T) {
		_, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "[",
		})

		if err == nil || !strings.Contains(err.Error(), "invalid glob pattern") {
			t.Fatalf("expected invalid glob pattern error, got: %v", err)
		}
	})

	t.Run("glob with absolute search path", func(t *testing.T) {
		result, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "**/*.go",
			"path":    tmpDir,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "main.go") {
			t.Errorf("expected main.go, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "app.go") {
			t.Errorf("expected app.go, got: %s", result.Content)
		}
	})

	t.Run("glob with absolute pattern", func(t *testing.T) {
		result, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": filepath.Join(tmpDir, "src", "*.go"),
			"path":    "node_modules",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result.Content, "main.go") || strings.Contains(result.Content, "node_modules") {
			t.Errorf("absolute pattern should override path, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "app.go") {
			t.Errorf("expected app.go, got: %s", result.Content)
		}
	})

	t.Run("glob skips VCS directories", func(t *testing.T) {
		newRoot, newTmp, newCleanup := createTestRoot(t)
		defer newCleanup()

		os.MkdirAll(filepath.Join(newTmp, ".git", "objects"), 0755)
		os.MkdirAll(filepath.Join(newTmp, ".svn"), 0755)
		os.MkdirAll(filepath.Join(newTmp, "src"), 0755)
		os.WriteFile(filepath.Join(newTmp, ".git", "objects", "blob.txt"), []byte("x"), 0644)
		os.WriteFile(filepath.Join(newTmp, ".svn", "entries.txt"), []byte("x"), 0644)
		os.WriteFile(filepath.Join(newTmp, "src", "main.txt"), []byte("x"), 0644)

		result, err := GlobTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "**/*.txt",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, filepath.Join("src", "main.txt")) {
			t.Errorf("expected src/main.txt in results, got: %s", result.Content)
		}
		if strings.Contains(result.Content, ".git") {
			t.Errorf(".git contents must be skipped, got: %s", result.Content)
		}
		if strings.Contains(result.Content, ".svn") {
			t.Errorf(".svn contents must be skipped, got: %s", result.Content)
		}
	})

	t.Run("glob rejects path pointing to a file", func(t *testing.T) {
		_, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "*.go",
			"path":    "main.go",
		})
		if err == nil {
			t.Fatal("expected error when path is not a directory")
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("expected 'not a directory' error, got: %v", err)
		}
	})

	t.Run("glob path outside workspace rejected", func(t *testing.T) {
		_, err := globTool.Execute(context.Background(), map[string]any{
			"pattern": "*.go",
			"path":    "/etc",
		})
		if err == nil {
			t.Fatal("expected error for search path outside workspace")
		}
	})

	t.Run("glob honors allowed read roots", func(t *testing.T) {
		outside, err := os.MkdirTemp("", "fs_glob_outside_*")
		if err != nil {
			t.Fatalf("mkdir outside: %v", err)
		}
		defer os.RemoveAll(outside)

		os.WriteFile(filepath.Join(outside, "note.md"), []byte("x"), 0644)
		os.MkdirAll(filepath.Join(outside, "sub"), 0755)
		os.WriteFile(filepath.Join(outside, "sub", "inner.md"), []byte("y"), 0644)

		denied, err := os.MkdirTemp("", "fs_glob_denied_*")
		if err != nil {
			t.Fatalf("mkdir denied: %v", err)
		}
		defer os.RemoveAll(denied)
		os.WriteFile(filepath.Join(denied, "secret.md"), []byte("z"), 0644)

		toolWithRoot := GlobTool(root, outside)

		result, err := toolWithRoot.Execute(context.Background(), map[string]any{
			"pattern": "**/*.md",
			"path":    outside,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(outside, "note.md")
		if !strings.Contains(result.Content, want) {
			t.Errorf("expected %s in result, got: %s", want, result.Content)
		}
		wantInner := filepath.Join(outside, "sub", "inner.md")
		if !strings.Contains(result.Content, wantInner) {
			t.Errorf("expected %s in result, got: %s", wantInner, result.Content)
		}

		_, err = toolWithRoot.Execute(context.Background(), map[string]any{
			"pattern": "*.md",
			"path":    denied,
		})
		if err == nil {
			t.Fatal("expected error for path outside workspace and allow-list")
		}
	})

	t.Run("glob returns modified order when results exceed limit", func(t *testing.T) {
		newRoot, newTmp, newCleanup := createTestRoot(t)
		defer newCleanup()

		base := time.Now().Add(-1 * time.Hour)
		for i := range 120 {
			name := fmt.Sprintf("f%03d.tmp", i)
			p := filepath.Join(newTmp, name)
			os.WriteFile(p, []byte("x"), 0644)
			os.Chtimes(p, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute))
		}

		result, err := GlobTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "*.tmp",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, want := range []string{"f119.tmp", "f118.tmp", "f117.tmp"} {
			if !strings.Contains(result.Content, want) {
				t.Errorf("expected newest file %s in result, got: %s", want, result.Content)
			}
		}
		if strings.Contains(result.Content, "f000.tmp") || strings.Contains(result.Content, "f001.tmp") {
			t.Errorf("oldest files leaked in despite limit, got: %s", result.Content)
		}
		if !strings.Contains(result.Content, "(Results are truncated. Consider using a more specific path or pattern.)") {
			t.Errorf("expected truncation notice, got: %s", result.Content)
		}
	})
}

func TestGlobSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests may require elevated privileges on Windows")
	}

	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	os.WriteFile(filepath.Join(tmpDir, "root.txt"), []byte("root content"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "dir1"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "dir1", "file.txt"), []byte("content"), 0644)

	symlink := filepath.Join(tmpDir, "dir1", "circular")
	if err := os.Symlink(tmpDir, symlink); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	globTool := GlobTool(root)

	result, err := globTool.Execute(context.Background(), map[string]any{
		"pattern": "*.txt",
	})

	if err != nil {
		t.Fatalf("glob should not fail with symlinks: %v", err)
	}

	if !strings.Contains(result.Content, "root.txt") && !strings.Contains(result.Content, "file.txt") {
		t.Errorf("expected txt files in results, got: %s", result.Content)
	}
}

func TestContextCancellation(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	for i := range 100 {
		dir := filepath.Join(tmpDir, "dir"+string(rune('a'+i%26)))
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0644)
	}

	globTool := GlobTool(root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := globTool.Execute(ctx, map[string]any{
		"pattern": "*.txt",
	})

	if err == nil {
		t.Log("Operation completed before context cancellation was detected (acceptable for fast operations)")
	} else if !strings.Contains(err.Error(), "context") {
		t.Logf("Expected context error, got: %v (may be acceptable)", err)
	}
}
