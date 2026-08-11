package fs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestFreshnessDetectsExternalChangesOnce(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	path := filepath.Join(tmpDir, "watched.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}

	freshness := fs.NewFreshness(root)
	tools := fs.Tools(root, &fs.Options{
		AllowedReadRoots:  []string{tmpDir},
		AllowedWriteRoots: []string{tmpDir},
		Freshness:         freshness,
	})
	byName := map[string]func(map[string]any) (string, error){}
	for _, tl := range tools {
		byName[tl.Name] = func(args map[string]any) (string, error) {
			result, err := tl.Execute(t.Context(), args)
			return result.Content, err
		}
	}

	if _, err := byName["read"](map[string]any{"file_path": path}); err != nil {
		t.Fatal(err)
	}
	if changed := freshness.Changed(); len(changed) != 0 {
		t.Fatalf("changed after own read = %v", changed)
	}

	if err := os.WriteFile(path, []byte("edited externally\n"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(2 * time.Second)
	os.Chtimes(path, past, past)

	changed := freshness.Changed()
	if len(changed) != 1 || changed[0] != path {
		t.Fatalf("changed = %v, want [%s]", changed, path)
	}
	if changed := freshness.Changed(); len(changed) != 0 {
		t.Fatalf("second sweep = %v, want empty", changed)
	}

	if _, err := byName["read"](map[string]any{"file_path": path}); err != nil {
		t.Fatal(err)
	}
	if _, err := byName["edit"](map[string]any{
		"file_path": path, "old_string": "edited externally", "new_string": "edited by tool",
	}); err != nil {
		t.Fatal(err)
	}
	if changed := freshness.Changed(); len(changed) != 0 {
		t.Fatalf("changed after own edit = %v", changed)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	changed = freshness.Changed()
	if len(changed) != 1 || changed[0] != path+" (deleted)" {
		t.Fatalf("changed after delete = %v", changed)
	}
	if changed := freshness.Changed(); len(changed) != 0 {
		t.Fatalf("sweep after delete = %v, want empty", changed)
	}
}

func TestEditRejectsStaleFileAfterExternalChange(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	path := filepath.Join(tmpDir, "stale.txt")
	os.WriteFile(path, []byte("alpha beta\n"), 0644)

	freshness := fs.NewFreshness(root)
	tools := fs.Tools(root, &fs.Options{
		AllowedReadRoots:  []string{tmpDir},
		AllowedWriteRoots: []string{tmpDir},
		Freshness:         freshness,
	})
	byName := map[string]func(map[string]any) (string, error){}
	for _, tl := range tools {
		byName[tl.Name] = func(args map[string]any) (string, error) {
			result, err := tl.Execute(t.Context(), args)
			return result.Content, err
		}
	}

	if _, err := byName["read"](map[string]any{"file_path": path}); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(path, []byte("alpha beta gamma\n"), 0644)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(path, future, future)

	_, err := byName["edit"](map[string]any{"file_path": path, "old_string": "alpha", "new_string": "omega"})
	if err == nil || !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("stale edit err = %v, want changed-on-disk rejection", err)
	}

	if _, err := byName["read"](map[string]any{"file_path": path}); err != nil {
		t.Fatal(err)
	}
	if _, err := byName["edit"](map[string]any{"file_path": path, "old_string": "alpha", "new_string": "omega"}); err != nil {
		t.Fatalf("edit after re-read err = %v", err)
	}
}
