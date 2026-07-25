package pi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestToolTitleBashCommand(t *testing.T) {
	if got := toolTitle("bash", map[string]any{"command": "echo hi"}); got != "echo hi" {
		t.Errorf("bash command title = %q", got)
	}
	if got := toolTitle("bash", map[string]any{"cmd": "ls"}); got != "ls" {
		t.Errorf("bash cmd title = %q", got)
	}
	if got := toolTitle("bash", nil); got != "bash" {
		t.Errorf("bash without args = %q", got)
	}
	if got := toolTitle("read", map[string]any{"command": "x"}); got != "read" {
		t.Errorf("non-bash = %q", got)
	}
	if got := toolTitle("", nil); got != "tool" {
		t.Errorf("empty name = %q", got)
	}
}

func TestEditOldTexts(t *testing.T) {
	got := editOldTexts(map[string]any{"oldText": "top", "newText": "x"})
	if len(got) != 1 || got[0] != "top" {
		t.Errorf("top-level = %v", got)
	}

	got = editOldTexts(map[string]any{"edits": []any{
		map[string]any{"oldText": "a", "newText": "b"},
		map[string]any{"oldText": "c", "newText": "d"},
	}})
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("edits array = %v", got)
	}

	got = editOldTexts(map[string]any{"edits": `[{"oldText":"s","newText":"t"}]`})
	if len(got) != 1 || got[0] != "s" {
		t.Errorf("stringified edits = %v", got)
	}

	if got = editOldTexts(map[string]any{}); len(got) != 0 {
		t.Errorf("empty = %v", got)
	}
}

func TestFindUniqueLine(t *testing.T) {
	text := "alpha\nbeta\ngamma\nbeta\n"
	if line := findUniqueLine(text, "gamma"); line == nil || *line != 3 {
		t.Errorf("unique = %v", line)
	}
	if line := findUniqueLine(text, "beta"); line != nil {
		t.Errorf("duplicate should be nil, got %v", line)
	}
	if line := findUniqueLine(text, "missing"); line != nil {
		t.Errorf("missing should be nil, got %v", line)
	}
	if line := findUniqueLine(text, ""); line != nil {
		t.Errorf("empty needle should be nil, got %v", line)
	}
}

func TestDeleteSessionIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abc.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"abc","cwd":"/x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New(Options{SessionsDir: dir})
	if _, err := a.UnstableDeleteSession(context.Background(), acp.UnstableDeleteSessionRequest{SessionId: "abc"}); err != nil {
		t.Fatalf("delete existing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session file should be removed, stat err = %v", err)
	}
	if _, err := a.UnstableDeleteSession(context.Background(), acp.UnstableDeleteSessionRequest{SessionId: "abc"}); err != nil {
		t.Fatalf("delete missing should be idempotent: %v", err)
	}
	if _, err := a.UnstableDeleteSession(context.Background(), acp.UnstableDeleteSessionRequest{SessionId: "never-existed"}); err != nil {
		t.Fatalf("delete unknown should be idempotent: %v", err)
	}
}
