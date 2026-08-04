package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/acp-go-sdk"
)

type bufferWriteCloser struct{ bytes.Buffer }

func (b *bufferWriteCloser) Close() error { return nil }

func TestTurnWaitsForAgentSettled(t *testing.T) {
	turn := &turn{
		sess: &session{},
		done: make(chan turnResult, 1),
	}
	turn.handle(json.RawMessage(`{"type":"agent_end"}`))
	select {
	case <-turn.done:
		t.Fatal("agent_end resolved the turn before Pi settled")
	default:
	}

	turn.handle(json.RawMessage(`{"type":"agent_settled"}`))
	select {
	case result := <-turn.done:
		if result.err != nil || result.stop != acp.StopReasonEndTurn {
			t.Fatalf("settled result = %+v", result)
		}
	default:
		t.Fatal("agent_settled did not resolve the turn")
	}
}

func TestExtensionUIOnlyRespondsToDialogRequests(t *testing.T) {
	writer := &bufferWriteCloser{}
	proc := &process{stdin: writer, done: make(chan struct{})}
	turn := &turn{sess: &session{proc: proc}}

	turn.handleExtensionUI(json.RawMessage(`{"type":"extension_ui_request","id":"status","method":"setStatus"}`))
	if writer.Len() != 0 {
		t.Fatalf("fire-and-forget UI request received a response: %q", writer.String())
	}

	turn.handleExtensionUI(json.RawMessage(`{"type":"extension_ui_request","id":"input","method":"input"}`))
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(writer.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	if response["type"] != "extension_ui_response" || response["id"] != "input" || response["cancelled"] != true {
		t.Fatalf("input fallback response = %#v", response)
	}
}

func TestIdleExtensionDialogIsCancelled(t *testing.T) {
	writer := &bufferWriteCloser{}
	proc := &process{stdin: writer, done: make(chan struct{})}
	proc.handleIdleEvent(json.RawMessage(`{"type":"extension_ui_request","id":"startup","method":"confirm"}`))

	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(writer.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	if response["id"] != "startup" || response["cancelled"] != true {
		t.Fatalf("startup dialog fallback = %#v", response)
	}
}

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
