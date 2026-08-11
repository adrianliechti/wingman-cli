package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestEditTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	editTool := EditTool(root)

	t.Run("simple edit", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "edit_test.txt")
		os.WriteFile(testFile, []byte("hello world"), 0644)

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"file_path": "edit_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		result, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "edit_test.txt",
			"old_string": "world",
			"new_string": "universe",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "Successfully") {
			t.Errorf("expected success message, got: %s", result.Content)
		}

		content, _ := os.ReadFile(testFile)

		if string(content) != "hello universe" {
			t.Errorf("expected 'hello universe', got: %s", content)
		}
	})

	t.Run("edit can create file with empty old string", func(t *testing.T) {
		result, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "created_by_edit.txt",
			"old_string": "",
			"new_string": "created content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Content, "Successfully") {
			t.Errorf("expected success message, got: %s", result.Content)
		}

		content, err := os.ReadFile(filepath.Join(tmpDir, "created_by_edit.txt"))
		if err != nil {
			t.Fatalf("failed to read created file: %v", err)
		}
		if string(content) != "created content" {
			t.Errorf("expected created content, got: %s", content)
		}
	})

	t.Run("edit rejects empty old string on non-empty file", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "nonempty_empty_old.txt")
		os.WriteFile(testFile, []byte("existing"), 0644)

		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "nonempty_empty_old.txt",
			"old_string": "",
			"new_string": "replacement",
		})

		if err == nil || !strings.Contains(err.Error(), "already has content") {
			t.Fatalf("expected non-empty file error, got: %v", err)
		}
	})

	t.Run("edit preserves curly quote style", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "curly_quotes.txt")
		os.WriteFile(testFile, []byte("title: “Hello”\n"), 0644)

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"file_path": "curly_quotes.txt",
		})
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
			"file_path":  "curly_quotes.txt",
			"old_string": `"Hello"`,
			"new_string": `"World"`,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, err := os.ReadFile(testFile)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if string(content) != "title: “World”\n" {
			t.Errorf("expected curly quote style preserved, got: %s", content)
		}
	})

	t.Run("edit preserves CRLF line endings", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "crlf_test.txt")
		os.WriteFile(testFile, []byte("line1\r\nline2\r\nline3"), 0644)

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"file_path": "crlf_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
			"file_path":  "crlf_test.txt",
			"old_string": "line2",
			"new_string": "modified",
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
			"file_path": "fuzzy_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
			"file_path":  "fuzzy_test.txt",
			"old_string": "hello\nworld",
			"new_string": "goodbye\nworld",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, _ := os.ReadFile(testFile)

		if string(content) != "goodbye\nworld" {
			t.Errorf("expected fuzzy match to replace whole match, got: %s", content)
		}
	})

	t.Run("edit fails for non-unique match", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "duplicate_test.txt")
		os.WriteFile(testFile, []byte("foo bar foo"), 0644)

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"file_path": "duplicate_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
			"file_path":  "duplicate_test.txt",
			"old_string": "foo",
			"new_string": "baz",
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
			"file_path": "nomatch_test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
			"file_path":  "nomatch_test.txt",
			"old_string": "xyz",
			"new_string": "abc",
		})

		if err == nil {
			t.Error("expected error for no match")
		}
	})

	t.Run("edit rejects legacy old_text params", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "legacy_params.txt")
		os.WriteFile(testFile, []byte("hello world"), 0644)

		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path": "legacy_params.txt",
			"old_text":  "world",
			"new_text":  "universe",
		})

		if err == nil || !strings.Contains(err.Error(), "old_string is required") {
			t.Fatalf("expected old_string required error, got: %v", err)
		}
	})

	t.Run("edit rejects identical replacement before matching", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "identical_test.txt")
		os.WriteFile(testFile, []byte("hello world"), 0644)

		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "identical_test.txt",
			"old_string": "world",
			"new_string": "world",
		})

		if err == nil || !strings.Contains(err.Error(), "identical") {
			t.Fatalf("expected identical replacement error, got: %v", err)
		}
	})

	t.Run("edit fuzzy replace_all must make progress", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "fuzzy_replace_all.txt")
		os.WriteFile(testFile, []byte("hello   \nworld\nhello   \nworld"), 0644)

		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":   "fuzzy_replace_all.txt",
			"old_string":  "hello\nworld",
			"new_string":  "hello   \nworld",
			"replace_all": true,
		})

		if err == nil || !strings.Contains(err.Error(), "made no progress") {
			t.Fatalf("expected no-progress error, got: %v", err)
		}
	})

	t.Run("edit replace_all replaces every occurrence", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "replace_all.txt")
		os.WriteFile(testFile, []byte("foo bar foo baz foo"), 0644)

		_, err := ReadTool(root).Execute(context.Background(), map[string]any{
			"file_path": "replace_all.txt",
		})
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = editTool.Execute(context.Background(), map[string]any{
			"file_path":   "replace_all.txt",
			"old_string":  "foo",
			"new_string":  "qux",
			"replace_all": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, _ := os.ReadFile(testFile)
		if string(content) != "qux bar qux baz qux" {
			t.Errorf("expected all 'foo' replaced with 'qux', got: %s", content)
		}
	})

	t.Run("edit rejects file exceeding MaxEditFileBytes", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "huge.txt")

		huge := strings.Repeat("a", MaxEditFileBytes+1)
		os.WriteFile(testFile, []byte(huge), 0644)

		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "huge.txt",
			"old_string": "a",
			"new_string": "b",
		})

		if err == nil || !strings.Contains(err.Error(), "capped at") {
			t.Fatalf("expected size cap error, got: %v", err)
		}
	})

	t.Run("edit on non-existent file with non-empty old_string fails", func(t *testing.T) {
		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "does_not_exist.txt",
			"old_string": "something",
			"new_string": "else",
		})

		if err == nil {
			t.Fatal("expected error for non-existent file with non-empty old_string")
		}
	})

	t.Run("edit rejects directory path", func(t *testing.T) {
		os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "subdir",
			"old_string": "anything",
			"new_string": "else",
		})
		if err == nil || !strings.Contains(err.Error(), "is a directory") {
			t.Fatalf("expected directory error, got: %v", err)
		}
	})

	t.Run("edit does not fuzzy match whitespace-only old string", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "whitespace_old.txt")
		os.WriteFile(testFile, []byte("hello\nworld"), 0644)

		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "whitespace_old.txt",
			"old_string": "   ",
			"new_string": "x",
		})

		if err == nil || !strings.Contains(err.Error(), "could not find old_string") {
			t.Fatalf("expected no match error, got: %v", err)
		}
	})

	t.Run("batched edits apply in order", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "batched.txt")
		os.WriteFile(testFile, []byte("alpha\nbeta\ngamma\n"), 0644)

		result, err := editTool.Execute(context.Background(), map[string]any{
			"file_path": "batched.txt",
			"edits": []any{
				map[string]any{"old_string": "alpha", "new_string": "one"},
				map[string]any{"old_string": "beta", "new_string": "two"},
				map[string]any{"old_string": "one\ntwo", "new_string": "one\ntwo\nthree"},
			},
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Content, "3 edits") {
			t.Errorf("expected batched success message, got: %s", result.Content)
		}

		content, _ := os.ReadFile(testFile)
		if string(content) != "one\ntwo\nthree\ngamma\n" {
			t.Errorf("unexpected content: %q", content)
		}
	})

	t.Run("batched edits are atomic on failure", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "batched_atomic.txt")
		os.WriteFile(testFile, []byte("alpha\nbeta\n"), 0644)

		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path": "batched_atomic.txt",
			"edits": []any{
				map[string]any{"old_string": "alpha", "new_string": "one"},
				map[string]any{"old_string": "missing", "new_string": "x"},
			},
		})

		if err == nil || !strings.Contains(err.Error(), "edits[1]") {
			t.Fatalf("expected indexed error, got: %v", err)
		}

		content, _ := os.ReadFile(testFile)
		if string(content) != "alpha\nbeta\n" {
			t.Errorf("file modified despite failed batch: %q", content)
		}
	})

	t.Run("batched edits reject mixing with old_string", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "batched_mixed.txt")
		os.WriteFile(testFile, []byte("alpha\n"), 0644)

		_, err := editTool.Execute(context.Background(), map[string]any{
			"file_path":  "batched_mixed.txt",
			"old_string": "alpha",
			"new_string": "one",
			"edits": []any{
				map[string]any{"old_string": "alpha", "new_string": "one"},
			},
		})

		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("expected mixing error, got: %v", err)
		}
	})

}
