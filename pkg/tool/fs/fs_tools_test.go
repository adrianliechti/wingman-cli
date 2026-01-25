package fs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

// createTestEnvironment creates a test environment with a temporary directory
func createTestEnvironment(t *testing.T) (*tool.Environment, string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "fs_test_*")

	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	root, err := os.OpenRoot(tmpDir)

	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open root: %v", err)
	}

	env := &tool.Environment{
		Root: root,
	}

	cleanup := func() {
		root.Close()
		os.RemoveAll(tmpDir)
	}

	return env, tmpDir, cleanup
}

func TestReadTool(t *testing.T) {
	env, tmpDir, cleanup := createTestEnvironment(t)
	defer cleanup()

	// Create test file
	content := "line1\nline2\nline3\nline4\nline5"
	testFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	readTool := ReadTool()

	t.Run("read entire file", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), env, map[string]any{
			"path": "test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "line1") || !strings.Contains(result, "line5") {
			t.Errorf("expected full content, got: %s", result)
		}
	})

	t.Run("read with offset", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), env, map[string]any{
			"path":   "test.txt",
			"offset": float64(3),
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result, "line1") || strings.Contains(result, "line2") {
			t.Errorf("offset should skip first lines, got: %s", result)
		}

		if !strings.Contains(result, "line3") {
			t.Errorf("should contain line3, got: %s", result)
		}
	})

	t.Run("read with limit", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), env, map[string]any{
			"path":  "test.txt",
			"limit": float64(2),
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "line1") {
			t.Errorf("should contain line1, got: %s", result)
		}
	})

	t.Run("read non-existent file", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), env, map[string]any{
			"path": "nonexistent.txt",
		})

		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("path outside workspace rejected", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), env, map[string]any{
			"path": "/etc/passwd",
		})

		if err == nil {
			t.Error("expected error for path outside workspace")
		}

		if !strings.Contains(err.Error(), "outside workspace") {
			t.Errorf("expected 'outside workspace' error, got: %v", err)
		}
	})

	t.Run("read with absolute path inside workspace", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), env, map[string]any{
			"path": testFile, // absolute path
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "line1") {
			t.Errorf("expected content, got: %s", result)
		}
	})
}

func TestWriteTool(t *testing.T) {
	env, tmpDir, cleanup := createTestEnvironment(t)
	defer cleanup()

	writeTool := WriteTool()

	t.Run("write new file", func(t *testing.T) {
		result, err := writeTool.Execute(context.Background(), env, map[string]any{
			"path":    "newfile.txt",
			"content": "hello world",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "Successfully") {
			t.Errorf("expected success message, got: %s", result)
		}

		// Verify file was created
		content, err := os.ReadFile(filepath.Join(tmpDir, "newfile.txt"))

		if err != nil {
			t.Fatalf("failed to read created file: %v", err)
		}

		if string(content) != "hello world" {
			t.Errorf("expected 'hello world', got: %s", content)
		}
	})

	t.Run("write with nested directory", func(t *testing.T) {
		_, err := writeTool.Execute(context.Background(), env, map[string]any{
			"path":    "subdir/nested/file.txt",
			"content": "nested content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify file was created
		content, err := os.ReadFile(filepath.Join(tmpDir, "subdir", "nested", "file.txt"))

		if err != nil {
			t.Fatalf("failed to read created file: %v", err)
		}

		if string(content) != "nested content" {
			t.Errorf("expected 'nested content', got: %s", content)
		}
	})

	t.Run("overwrite existing file", func(t *testing.T) {
		// First write
		_, err := writeTool.Execute(context.Background(), env, map[string]any{
			"path":    "overwrite.txt",
			"content": "original",
		})

		if err != nil {
			t.Fatalf("unexpected error on first write: %v", err)
		}

		// Overwrite
		_, err = writeTool.Execute(context.Background(), env, map[string]any{
			"path":    "overwrite.txt",
			"content": "updated",
		})

		if err != nil {
			t.Fatalf("unexpected error on overwrite: %v", err)
		}

		content, err := os.ReadFile(filepath.Join(tmpDir, "overwrite.txt"))

		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		if string(content) != "updated" {
			t.Errorf("expected 'updated', got: %s", content)
		}
	})

	t.Run("path outside workspace rejected", func(t *testing.T) {
		_, err := writeTool.Execute(context.Background(), env, map[string]any{
			"path":    "/tmp/outside.txt",
			"content": "should fail",
		})

		if err == nil {
			t.Error("expected error for path outside workspace")
		}
	})
}

func TestEditTool(t *testing.T) {
	env, tmpDir, cleanup := createTestEnvironment(t)
	defer cleanup()

	editTool := EditTool()

	t.Run("simple edit", func(t *testing.T) {
		// Create test file
		testFile := filepath.Join(tmpDir, "edit_test.txt")
		os.WriteFile(testFile, []byte("hello world"), 0644)

		result, err := editTool.Execute(context.Background(), env, map[string]any{
			"path":     "edit_test.txt",
			"old_text": "world",
			"new_text": "universe",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "Successfully") {
			t.Errorf("expected success message, got: %s", result)
		}

		content, _ := os.ReadFile(testFile)

		if string(content) != "hello universe" {
			t.Errorf("expected 'hello universe', got: %s", content)
		}
	})

	t.Run("edit preserves CRLF line endings", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "crlf_test.txt")
		os.WriteFile(testFile, []byte("line1\r\nline2\r\nline3"), 0644)

		_, err := editTool.Execute(context.Background(), env, map[string]any{
			"path":     "crlf_test.txt",
			"old_text": "line2",
			"new_text": "modified",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, _ := os.ReadFile(testFile)

		if !strings.Contains(string(content), "\r\n") {
			t.Error("CRLF line endings should be preserved")
		}
	})

	t.Run("edit with fuzzy match (trailing whitespace)", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "fuzzy_test.txt")
		os.WriteFile(testFile, []byte("hello   \nworld"), 0644)

		_, err := editTool.Execute(context.Background(), env, map[string]any{
			"path":     "fuzzy_test.txt",
			"old_text": "hello\nworld",
			"new_text": "goodbye\nworld",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, _ := os.ReadFile(testFile)

		if !strings.Contains(string(content), "goodbye") {
			t.Errorf("expected fuzzy match to work, got: %s", content)
		}
	})

	t.Run("edit fails for non-unique match", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "duplicate_test.txt")
		os.WriteFile(testFile, []byte("foo bar foo"), 0644)

		_, err := editTool.Execute(context.Background(), env, map[string]any{
			"path":     "duplicate_test.txt",
			"old_text": "foo",
			"new_text": "baz",
		})

		if err == nil {
			t.Error("expected error for non-unique match")
		}

		if !strings.Contains(err.Error(), "occurrences") {
			t.Errorf("expected 'occurrences' in error, got: %v", err)
		}
	})

	t.Run("edit fails for no match", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "nomatch_test.txt")
		os.WriteFile(testFile, []byte("hello world"), 0644)

		_, err := editTool.Execute(context.Background(), env, map[string]any{
			"path":     "nomatch_test.txt",
			"old_text": "xyz",
			"new_text": "abc",
		})

		if err == nil {
			t.Error("expected error for no match")
		}
	})
}

func TestLsTool(t *testing.T) {
	env, tmpDir, cleanup := createTestEnvironment(t)
	defer cleanup()

	// Create test directory structure
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, ".hidden"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "subdir", "nested.txt"), []byte("content"), 0644)

	lsTool := LsTool()

	t.Run("list current directory", func(t *testing.T) {
		result, err := lsTool.Execute(context.Background(), env, map[string]any{})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "file1.txt") {
			t.Errorf("expected file1.txt, got: %s", result)
		}

		if !strings.Contains(result, "subdir/") {
			t.Errorf("expected subdir/ (with trailing slash), got: %s", result)
		}
	})

	t.Run("list includes hidden files", func(t *testing.T) {
		result, err := lsTool.Execute(context.Background(), env, map[string]any{})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, ".hidden") {
			t.Errorf("expected .hidden file, got: %s", result)
		}
	})

	t.Run("list subdirectory", func(t *testing.T) {
		result, err := lsTool.Execute(context.Background(), env, map[string]any{
			"path": "subdir",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "nested.txt") {
			t.Errorf("expected nested.txt, got: %s", result)
		}
	})

	t.Run("list empty directory", func(t *testing.T) {
		os.MkdirAll(filepath.Join(tmpDir, "empty"), 0755)
		result, err := lsTool.Execute(context.Background(), env, map[string]any{
			"path": "empty",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "empty directory") {
			t.Errorf("expected empty directory message, got: %s", result)
		}
	})

	t.Run("list non-existent path", func(t *testing.T) {
		_, err := lsTool.Execute(context.Background(), env, map[string]any{
			"path": "nonexistent",
		})

		if err == nil {
			t.Error("expected error for non-existent path")
		}
	})

	t.Run("list file instead of directory", func(t *testing.T) {
		_, err := lsTool.Execute(context.Background(), env, map[string]any{
			"path": "file1.txt",
		})

		if err == nil {
			t.Error("expected error when listing a file")
		}
	})
}

func TestFindTool(t *testing.T) {
	env, tmpDir, cleanup := createTestEnvironment(t)
	defer cleanup()

	// Create test directory structure
	os.MkdirAll(filepath.Join(tmpDir, "src", "pkg"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "node_modules", "dep"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "app.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "pkg", "util.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "app.ts"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "node_modules", "dep", "index.js"), []byte("content"), 0644)

	// Create .gitignore
	os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("*.log\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "debug.log"), []byte("content"), 0644)

	findTool := FindTool()

	t.Run("find all go files", func(t *testing.T) {
		result, err := findTool.Execute(context.Background(), env, map[string]any{
			"pattern": "**/*.go",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "main.go") {
			t.Errorf("expected main.go, got: %s", result)
		}

		if !strings.Contains(result, "app.go") {
			t.Errorf("expected app.go, got: %s", result)
		}

		if !strings.Contains(result, "util.go") {
			t.Errorf("expected util.go, got: %s", result)
		}
	})

	t.Run("find excludes node_modules", func(t *testing.T) {
		result, err := findTool.Execute(context.Background(), env, map[string]any{
			"pattern": "**/*.js",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result, "node_modules") {
			t.Errorf("should not include node_modules, got: %s", result)
		}
	})

	t.Run("find respects gitignore", func(t *testing.T) {
		result, err := findTool.Execute(context.Background(), env, map[string]any{
			"pattern": "*.log",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result, "debug.log") {
			t.Errorf("should respect gitignore and exclude .log files, got: %s", result)
		}
	})

	t.Run("find in subdirectory", func(t *testing.T) {
		result, err := findTool.Execute(context.Background(), env, map[string]any{
			"pattern": "*.go",
			"path":    "src",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result, "main.go") {
			t.Errorf("should not include files outside src, got: %s", result)
		}

		if !strings.Contains(result, "app.go") {
			t.Errorf("expected app.go, got: %s", result)
		}
	})

	t.Run("find with no matches", func(t *testing.T) {
		result, err := findTool.Execute(context.Background(), env, map[string]any{
			"pattern": "*.xyz",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "No files found") {
			t.Errorf("expected 'No files found', got: %s", result)
		}
	})

	t.Run("find with absolute path", func(t *testing.T) {
		result, err := findTool.Execute(context.Background(), env, map[string]any{
			"pattern": "**/*.go",
			"path":    tmpDir, // absolute path to workspace root
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "main.go") {
			t.Errorf("expected main.go, got: %s", result)
		}

		if !strings.Contains(result, "app.go") {
			t.Errorf("expected app.go, got: %s", result)
		}
	})
}

func TestGrepTool(t *testing.T) {
	env, tmpDir, cleanup := createTestEnvironment(t)
	defer cleanup()

	// Create test files
	os.WriteFile(filepath.Join(tmpDir, "file1.go"), []byte("package main\n\nfunc Hello() {\n\treturn \"hello\"\n}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.go"), []byte("package util\n\nfunc World() {\n\treturn \"world\"\n}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# Hello World\nThis is a test."), 0644)

	grepTool := GrepTool()

	t.Run("grep simple pattern", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), env, map[string]any{
			"pattern": "func",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "file1.go") {
			t.Errorf("expected file1.go in results, got: %s", result)
		}

		if !strings.Contains(result, "Hello") {
			t.Errorf("expected 'Hello' in results, got: %s", result)
		}
	})

	t.Run("grep with regex", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), env, map[string]any{
			"pattern": "func \\w+\\(",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "Hello()") || !strings.Contains(result, "World()") {
			t.Errorf("expected function matches, got: %s", result)
		}
	})

	t.Run("grep case insensitive", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), env, map[string]any{
			"pattern":    "HELLO",
			"ignoreCase": true,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "Hello") || !strings.Contains(result, "hello") {
			t.Errorf("expected case-insensitive matches, got: %s", result)
		}
	})

	t.Run("grep with glob filter", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), env, map[string]any{
			"pattern": "Hello",
			"glob":    "*.go",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result, "readme.md") {
			t.Errorf("should not include markdown files, got: %s", result)
		}
	})

	t.Run("grep with context lines", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), env, map[string]any{
			"pattern": "func Hello",
			"context": float64(1),
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should include the match line
		if !strings.Contains(result, "func Hello") {
			t.Errorf("expected match line, got: %s", result)
		}
		// Context should include lines around the match
		lines := strings.Split(result, "\n")

		if len(lines) < 2 {
			t.Errorf("expected multiple lines with context, got: %s", result)
		}
	})

	t.Run("grep no matches", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), env, map[string]any{
			"pattern": "zzz_no_match_zzz",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "No matches") {
			t.Errorf("expected 'No matches', got: %s", result)
		}
	})

	t.Run("grep single file", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), env, map[string]any{
			"pattern": "Hello",
			"path":    "readme.md",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "readme.md") {
			t.Errorf("expected readme.md match, got: %s", result)
		}

		if strings.Contains(result, "file1.go") {
			t.Errorf("should only search single file, got: %s", result)
		}
	})

	t.Run("grep with absolute path", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), env, map[string]any{
			"pattern": "func",
			"path":    tmpDir, // absolute path to workspace root
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "file1.go") {
			t.Errorf("expected file1.go in results, got: %s", result)
		}

		if !strings.Contains(result, "Hello") {
			t.Errorf("expected 'Hello' in results, got: %s", result)
		}
	})
}

func TestPathHandlingCrossplatform(t *testing.T) {
	env, tmpDir, cleanup := createTestEnvironment(t)
	defer cleanup()

	// Create nested structure
	os.MkdirAll(filepath.Join(tmpDir, "a", "b", "c"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "a", "b", "c", "file.txt"), []byte("content"), 0644)

	writeTool := WriteTool()
	readTool := ReadTool()

	t.Run("forward slash paths work", func(t *testing.T) {
		_, err := writeTool.Execute(context.Background(), env, map[string]any{
			"path":    "a/b/c/new.txt",
			"content": "test",
		})

		if err != nil {
			t.Fatalf("unexpected error with forward slashes: %v", err)
		}

		result, err := readTool.Execute(context.Background(), env, map[string]any{
			"path": "a/b/c/new.txt",
		})

		if err != nil {
			t.Fatalf("unexpected error reading: %v", err)
		}

		if !strings.Contains(result, "test") {
			t.Errorf("expected content, got: %s", result)
		}
	})

	if runtime.GOOS == "windows" {
		t.Run("backslash paths work on windows", func(t *testing.T) {
			_, err := writeTool.Execute(context.Background(), env, map[string]any{
				"path":    "a\\b\\c\\win.txt",
				"content": "windows",
			})

			if err != nil {
				t.Fatalf("unexpected error with backslashes: %v", err)
			}
		})
	}
}

func TestTools(t *testing.T) {
	tools := Tools()

	expectedNames := []string{"read", "write", "edit", "ls", "find", "grep"}

	if len(tools) != len(expectedNames) {
		t.Errorf("expected %d tools, got %d", len(expectedNames), len(tools))
	}

	names := make(map[string]bool)

	for _, tool := range tools {
		names[tool.Name] = true

		if tool.Description == "" {
			t.Errorf("tool %s has empty description", tool.Name)
		}

		if tool.Execute == nil {
			t.Errorf("tool %s has nil Execute function", tool.Name)
		}
	}

	for _, name := range expectedNames {
		if !names[name] {
			t.Errorf("missing expected tool: %s", name)
		}
	}
}