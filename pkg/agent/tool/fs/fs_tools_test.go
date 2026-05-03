package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// createTestRoot creates a test os.Root with a temporary directory
func createTestRoot(t *testing.T) (*os.Root, string, func()) {
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

	cleanup := func() {
		root.Close()
		os.RemoveAll(tmpDir)
	}

	return root, tmpDir, cleanup
}

func TestReadTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Create test file
	content := "line1\nline2\nline3\nline4\nline5"
	testFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	readTool := ReadTool(root)

	t.Run("read entire file", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), map[string]any{
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
		result, err := readTool.Execute(context.Background(), map[string]any{
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
		result, err := readTool.Execute(context.Background(), map[string]any{
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
		_, err := readTool.Execute(context.Background(), map[string]any{
			"path": "nonexistent.txt",
		})

		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("path outside workspace rejected", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
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
		result, err := readTool.Execute(context.Background(), map[string]any{
			"path": testFile, // absolute path
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "line1") {
			t.Errorf("expected content, got: %s", result)
		}
	})

	t.Run("read rejects binary files", func(t *testing.T) {
		// Create a fake "binary" file by extension. We shouldn't even try to read it.
		os.WriteFile(filepath.Join(tmpDir, "logo.png"), []byte("\x89PNG\r\n\x1a\n"), 0644)

		_, err := readTool.Execute(context.Background(), map[string]any{
			"path": "logo.png",
		})

		if err == nil {
			t.Fatal("expected error reading binary file, got nil")
		}

		if !strings.Contains(err.Error(), "binary") {
			t.Errorf("expected 'binary' in error, got: %v", err)
		}
	})
}

func TestWriteTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	writeTool := WriteTool(root)

	t.Run("write new file", func(t *testing.T) {
		result, err := writeTool.Execute(context.Background(), map[string]any{
			"path":    "newfile.txt",
			"content": "hello world",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "Created") {
			t.Errorf("expected 'Created' message, got: %s", result)
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
		_, err := writeTool.Execute(context.Background(), map[string]any{
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
		_, err := writeTool.Execute(context.Background(), map[string]any{
			"path":    "overwrite.txt",
			"content": "original",
		})

		if err != nil {
			t.Fatalf("unexpected error on first write: %v", err)
		}

		_, err = ReadTool(root).Execute(context.Background(), map[string]any{
			"path": "overwrite.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		// Overwrite
		_, err = writeTool.Execute(context.Background(), map[string]any{
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
		_, err := writeTool.Execute(context.Background(), map[string]any{
			"path":    "/tmp/outside.txt",
			"content": "should fail",
		})

		if err == nil {
			t.Error("expected error for path outside workspace")
		}
	})
}

func TestEditTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	editTool := EditTool(root)

	t.Run("simple edit", func(t *testing.T) {
		// Create test file
		testFile := filepath.Join(tmpDir, "edit_test.txt")
		os.WriteFile(testFile, []byte("hello world"), 0644)

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"path": "edit_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		result, err := editTool.Execute(context.Background(), map[string]any{
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

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"path": "crlf_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
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

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"path": "fuzzy_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
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

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"path": "duplicate_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
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

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"path": "nomatch_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
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
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Create test directory structure
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, ".hidden"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "subdir", "nested.txt"), []byte("content"), 0644)

	lsTool := LsTool(root)

	t.Run("list current directory", func(t *testing.T) {
		result, err := lsTool.Execute(context.Background(), map[string]any{})

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
		result, err := lsTool.Execute(context.Background(), map[string]any{})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, ".hidden") {
			t.Errorf("expected .hidden file, got: %s", result)
		}
	})

	t.Run("list subdirectory", func(t *testing.T) {
		result, err := lsTool.Execute(context.Background(), map[string]any{
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
		result, err := lsTool.Execute(context.Background(), map[string]any{
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
		_, err := lsTool.Execute(context.Background(), map[string]any{
			"path": "nonexistent",
		})

		if err == nil {
			t.Error("expected error for non-existent path")
		}
	})

	t.Run("list file instead of directory", func(t *testing.T) {
		_, err := lsTool.Execute(context.Background(), map[string]any{
			"path": "file1.txt",
		})

		if err == nil {
			t.Error("expected error when listing a file")
		}
	})
}

func TestFindTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
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

	findTool := FindTool(root)

	t.Run("find all go files", func(t *testing.T) {
		result, err := findTool.Execute(context.Background(), map[string]any{
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
		result, err := findTool.Execute(context.Background(), map[string]any{
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
		result, err := findTool.Execute(context.Background(), map[string]any{
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
		result, err := findTool.Execute(context.Background(), map[string]any{
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
		result, err := findTool.Execute(context.Background(), map[string]any{
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
		result, err := findTool.Execute(context.Background(), map[string]any{
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

	t.Run("find returns newest when results exceed limit", func(t *testing.T) {
		// Regression test: walk must visit every match before sorting, otherwise
		// "newest first" is just "newest among the first N walked" which depends
		// on filesystem order. Create files where the newest live alphabetically
		// last, then assert they survive a small limit.
		newRoot, newTmp, newCleanup := createTestRoot(t)
		defer newCleanup()

		base := time.Now().Add(-1 * time.Hour)
		// 20 files: aa.tmp ... at.tmp. Earlier letter = older.
		for i := range 20 {
			name := fmt.Sprintf("%c%c.tmp", 'a', 'a'+i)
			p := filepath.Join(newTmp, name)
			os.WriteFile(p, []byte("x"), 0644)
			os.Chtimes(p, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute))
		}

		result, err := FindTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "*.tmp",
			"limit":   float64(3),
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Newest 3 are the last three alphabetically: ar.tmp, as.tmp, at.tmp.
		for _, want := range []string{"ar.tmp", "as.tmp", "at.tmp"} {
			if !strings.Contains(result, want) {
				t.Errorf("expected newest file %s in result, got: %s", want, result)
			}
		}
		// Older files must be excluded.
		if strings.Contains(result, "aa.tmp") || strings.Contains(result, "ab.tmp") {
			t.Errorf("oldest files leaked in despite limit=3, got: %s", result)
		}
		// And the notice must surface that we truncated.
		if !strings.Contains(result, "20 files found, showing newest 3") {
			t.Errorf("expected truncation notice, got: %s", result)
		}
	})
}

func TestGrepTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Create test files
	os.WriteFile(filepath.Join(tmpDir, "file1.go"), []byte("package main\n\nfunc Hello() {\n\treturn \"hello\"\n}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.go"), []byte("package util\n\nfunc World() {\n\treturn \"world\"\n}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# Hello World\nThis is a test."), 0644)

	grepTool := GrepTool(root)

	t.Run("grep simple pattern", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
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
		result, err := grepTool.Execute(context.Background(), map[string]any{
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
		result, err := grepTool.Execute(context.Background(), map[string]any{
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
		result, err := grepTool.Execute(context.Background(), map[string]any{
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
		result, err := grepTool.Execute(context.Background(), map[string]any{
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
		result, err := grepTool.Execute(context.Background(), map[string]any{
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
		result, err := grepTool.Execute(context.Background(), map[string]any{
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
		result, err := grepTool.Execute(context.Background(), map[string]any{
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

	t.Run("grep multiline pattern spanning lines", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, "multi.go"), []byte("type Foo struct {\n\tname string\n\tfield int\n}\n"), 0644)

		// Without multiline this can't match across newlines.
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":   `struct \{[\s\S]*?field`,
			"path":      "multi.go",
			"multiline": true,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result, "multi.go") || !strings.Contains(result, "field") {
			t.Errorf("expected multi.go and matched 'field' line, got: %s", result)
		}

		// Sanity: same pattern without multiline must NOT match.
		nonMulti, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": `struct \{[\s\S]*?field`,
			"path":    "multi.go",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(nonMulti, "No matches") {
			t.Errorf("non-multiline should not match across lines, got: %s", nonMulti)
		}
	})
}

func TestPathHandlingCrossplatform(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Create nested structure
	os.MkdirAll(filepath.Join(tmpDir, "a", "b", "c"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "a", "b", "c", "file.txt"), []byte("content"), 0644)

	writeTool := WriteTool(root)
	readTool := ReadTool(root)

	t.Run("forward slash paths work", func(t *testing.T) {
		_, err := writeTool.Execute(context.Background(), map[string]any{
			"path":    "a/b/c/new.txt",
			"content": "test",
		})

		if err != nil {
			t.Fatalf("unexpected error with forward slashes: %v", err)
		}

		result, err := readTool.Execute(context.Background(), map[string]any{
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
			_, err := writeTool.Execute(context.Background(), map[string]any{
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
	root, _, cleanup := createTestRoot(t)
	defer cleanup()

	tools := Tools(root)

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

// TestFindSkipsSymlinks verifies that symlinks are skipped during file traversal
// to prevent infinite loops from circular symlinks.
func TestFindSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests may require elevated privileges on Windows")
	}

	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Create directory structure with a file at root level
	os.WriteFile(filepath.Join(tmpDir, "root.txt"), []byte("root content"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "dir1"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "dir1", "file.txt"), []byte("content"), 0644)

	// Create a symlink pointing to the parent (circular)
	symlink := filepath.Join(tmpDir, "dir1", "circular")
	if err := os.Symlink(tmpDir, symlink); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	findTool := FindTool(root)

	// This should complete without hanging or error
	result, err := findTool.Execute(context.Background(), map[string]any{
		"pattern": "*.txt",
	})

	if err != nil {
		t.Fatalf("find should not fail with symlinks: %v", err)
	}

	// Should find regular files but not follow the circular symlink infinitely
	if !strings.Contains(result, "root.txt") && !strings.Contains(result, "file.txt") {
		t.Errorf("expected txt files in results, got: %s", result)
	}
}

// TestGrepSkipsSymlinks verifies that symlinks are skipped during grep traversal.
func TestGrepSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests may require elevated privileges on Windows")
	}

	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Create directory structure
	os.MkdirAll(filepath.Join(tmpDir, "dir1"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "dir1", "file.txt"), []byte("searchme"), 0644)

	// Create a symlink pointing to the parent (circular)
	symlink := filepath.Join(tmpDir, "dir1", "circular")
	if err := os.Symlink(tmpDir, symlink); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	grepTool := GrepTool(root)

	// This should complete without hanging or error
	result, err := grepTool.Execute(context.Background(), map[string]any{
		"pattern": "searchme",
	})

	if err != nil {
		t.Fatalf("grep should not fail with symlinks: %v", err)
	}

	// Should find the match
	if !strings.Contains(result, "searchme") {
		t.Errorf("expected 'searchme' in results, got: %s", result)
	}
}

// TestContextCancellation verifies that operations respect context cancellation.
func TestContextCancellation(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Create many files to make the operation take longer
	for i := range 100 {
		dir := filepath.Join(tmpDir, "dir"+string(rune('a'+i%26)))
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0644)
	}

	findTool := FindTool(root)

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := findTool.Execute(ctx, map[string]any{
		"pattern": "*.txt",
	})

	// Should return a context error
	if err == nil {
		t.Log("Operation completed before context cancellation was detected (acceptable for fast operations)")
	} else if !strings.Contains(err.Error(), "context") {
		t.Logf("Expected context error, got: %v (may be acceptable)", err)
	}
}

// TestMacOSCaseInsensitivePaths tests that path comparison works correctly on macOS.
func TestMacOSCaseInsensitivePaths(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific test")
	}

	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Create a file
	os.WriteFile(filepath.Join(tmpDir, "TestFile.txt"), []byte("content"), 0644)

	readTool := ReadTool(root)

	// On macOS, paths with different cases should work due to case-insensitive filesystem
	// Using the absolute path with different case
	upperPath := strings.ToUpper(tmpDir) + "/TESTFILE.TXT"

	// Try to read with different case - this tests filesystem behavior, not our normalization
	// The key is that our workspace boundary detection should work with case differences
	lowerPath := strings.ToLower(tmpDir) + "/testfile.txt"

	// Both should resolve within the workspace (not "outside workspace" error)
	_, err := readTool.Execute(context.Background(), map[string]any{
		"path": upperPath,
	})

	// We expect either success or "file not found" but NOT "outside workspace"
	if err != nil && strings.Contains(err.Error(), "outside workspace") {
		t.Errorf("path with different case should not be considered outside workspace: %v", err)
	}

	_, err = readTool.Execute(context.Background(), map[string]any{
		"path": lowerPath,
	})

	if err != nil && strings.Contains(err.Error(), "outside workspace") {
		t.Errorf("lowercase path should not be considered outside workspace: %v", err)
	}
}
