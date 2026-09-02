package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchEditToolSchemaIsProviderNeutral(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	edit := batchEditTool(root, nil)
	if edit.Execute == nil {
		t.Fatal("edit must use standard structured tool calling")
	}
	properties, ok := edit.Parameters["properties"].(map[string]any)
	if !ok || len(properties) != 1 || properties["edits"] == nil {
		t.Fatalf("schema properties = %#v, want only edits", properties)
	}
}

func TestBatchEditToolAppliesCrossFileTransaction(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(directory, "existing.txt"), []byte("one two\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	result, err := batchEditTool(root, nil).Execute(context.Background(), map[string]any{"edits": []any{
		map[string]any{"file_path": "existing.txt", "old_string": "one", "new_string": "ONE"},
		map[string]any{"file_path": "existing.txt", "old_string": "two", "new_string": "TWO"},
		map[string]any{"file_path": "nested/created.txt", "old_string": "", "new_string": "created\n"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "3 edits across 2 files") {
		t.Fatalf("unexpected result: %q", result.Content)
	}
	if contents, _ := os.ReadFile(filepath.Join(directory, "existing.txt")); string(contents) != "ONE TWO\n" {
		t.Fatalf("existing contents = %q", contents)
	}
	if contents, _ := os.ReadFile(filepath.Join(directory, "nested", "created.txt")); string(contents) != "created\n" {
		t.Fatalf("created contents = %q", contents)
	}
	if info, err := os.Stat(filepath.Join(directory, "existing.txt")); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("existing mode = %v, %v", info, err)
	}
}

func TestBatchEditToolDoesNotPartiallyCommit(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path := filepath.Join(directory, "existing.txt")
	if err := os.WriteFile(path, []byte("unchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = batchEditTool(root, nil).Execute(context.Background(), map[string]any{"edits": []any{
		map[string]any{"file_path": "created.txt", "old_string": "", "new_string": "must not exist\n"},
		map[string]any{"file_path": "existing.txt", "old_string": "missing", "new_string": "replacement"},
	}})
	if err == nil || !strings.Contains(err.Error(), "no files changed") {
		t.Fatalf("expected atomic validation error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "created.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("created file exists after failed transaction: %v", statErr)
	}
	if contents, _ := os.ReadFile(path); string(contents) != "unchanged\n" {
		t.Fatalf("existing contents changed to %q", contents)
	}
}

func TestBatchEditToolCreatesEmptyFile(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, err = batchEditTool(root, nil).Execute(context.Background(), map[string]any{"edits": []any{
		map[string]any{"file_path": "empty.txt", "old_string": "", "new_string": ""},
	}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(directory, "empty.txt"))
	if err != nil || info.Size() != 0 {
		t.Fatalf("empty file = %v, %v", info, err)
	}
}

func TestBatchEditToolCanCommitAcrossAllowedRoot(t *testing.T) {
	workspace := t.TempDir()
	allowed := t.TempDir()
	root, err := os.OpenRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(workspace, "workspace.txt"), []byte("old workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(allowed, "memory.txt"), []byte("old memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = batchEditTool(root, nil, allowed).Execute(context.Background(), map[string]any{"edits": []any{
		map[string]any{"file_path": "workspace.txt", "old_string": "old", "new_string": "new"},
		map[string]any{"file_path": filepath.Join(allowed, "memory.txt"), "old_string": "old", "new_string": "new"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(workspace, "workspace.txt"), filepath.Join(allowed, "memory.txt")} {
		contents, err := os.ReadFile(path)
		if err != nil || !strings.HasPrefix(string(contents), "new") {
			t.Fatalf("%s = %q, %v", path, contents, err)
		}
	}
}

func TestBatchEditToolRejectsOutsidePathBeforeWriting(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	root, err := os.OpenRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, err = batchEditTool(root, nil).Execute(context.Background(), map[string]any{"edits": []any{
		map[string]any{"file_path": "created.txt", "old_string": "", "new_string": "must not exist"},
		map[string]any{"file_path": filepath.Join(outside, "escape.txt"), "old_string": "", "new_string": "escape"},
	}})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside-workspace error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "created.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("created file exists after rejected batch: %v", statErr)
	}
}
