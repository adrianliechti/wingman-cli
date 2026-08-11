package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestWriteTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	writeTool := WriteTool(root)

	t.Run("write new file", func(t *testing.T) {
		result, err := writeTool.Execute(context.Background(), map[string]any{
			"file_path": "newfile.txt",
			"content":   "hello world",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "Created") {
			t.Errorf("expected 'Created' message, got: %s", result.Content)
		}

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
			"file_path": "subdir/nested/file.txt",
			"content":   "nested content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, err := os.ReadFile(filepath.Join(tmpDir, "subdir", "nested", "file.txt"))

		if err != nil {
			t.Fatalf("failed to read created file: %v", err)
		}

		if string(content) != "nested content" {
			t.Errorf("expected 'nested content', got: %s", content)
		}
	})

	t.Run("overwrite existing file", func(t *testing.T) {
		_, err := writeTool.Execute(context.Background(), map[string]any{
			"file_path": "overwrite.txt",
			"content":   "original",
		})

		if err != nil {
			t.Fatalf("unexpected error on first write: %v", err)
		}

		_, err = ReadTool(root).Execute(context.Background(), map[string]any{
			"file_path": "overwrite.txt",
		})

		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}

		_, err = writeTool.Execute(context.Background(), map[string]any{
			"file_path": "overwrite.txt",
			"content":   "updated",
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
			"file_path": "/tmp/outside.txt",
			"content":   "should fail",
		})

		if err == nil {
			t.Error("expected error for path outside workspace")
		}
	})

	t.Run("write rejects directory path", func(t *testing.T) {
		_, err := writeTool.Execute(context.Background(), map[string]any{
			"file_path": ".",
			"content":   "should fail",
		})

		if err == nil || !strings.Contains(err.Error(), "directory") {
			t.Fatalf("expected directory error, got: %v", err)
		}
	})
}
