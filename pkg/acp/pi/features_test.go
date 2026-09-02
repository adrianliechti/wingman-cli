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

func TestFailedLoadKeepsExistingLiveSession(t *testing.T) {
	cwd := t.TempDir()
	sessionsDir := t.TempDir()
	const id = acp.SessionId("00000000-0000-4000-8000-000000000099")
	history := []byte(`{"type":"session","id":"` + string(id) + `","cwd":"` + cwd + `"}` + "\n")
	if err := os.WriteFile(filepath.Join(sessionsDir, "session.jsonl"), history, 0o600); err != nil {
		t.Fatal(err)
	}

	old := newSession(id, cwd, &process{})
	a := New(Options{Path: filepath.Join(t.TempDir(), "missing-pi"), SessionsDir: sessionsDir})
	a.storeSession(old)
	t.Cleanup(func() { _ = a.Close() })

	_, err := a.LoadSession(context.Background(), acp.LoadSessionRequest{
		SessionId: id, Cwd: cwd, McpServers: []acp.McpServer{},
	})
	if err == nil {
		t.Fatal("load with a missing Pi executable succeeded")
	}
	if got := a.lookup(id); got != old || old.isClosed() {
		t.Fatalf("failed load replaced or closed the live session: got=%p old=%p", got, old)
	}
}

func TestPresentToolTitles(t *testing.T) {
	if got := presentTool("bash", map[string]any{"command": "echo hi"}, "").title; got != "Run command" {
		t.Errorf("bash command title = %q", got)
	}
	if got := presentTool("bash", map[string]any{"cmd": "ls"}, "").title; got != "Run command" {
		t.Errorf("bash cmd title = %q", got)
	}
	if got := presentTool("bash", nil, "").title; got != "Run command" {
		t.Errorf("bash without args = %q", got)
	}
	if got := presentTool("read", map[string]any{"command": "x"}, "").title; got != "Read file" {
		t.Errorf("non-bash = %q", got)
	}
	if got := presentTool("", nil, "").title; got != "Tool call" {
		t.Errorf("empty name = %q", got)
	}
}

func TestPresentToolKeepsPathOnlyAsLocation(t *testing.T) {
	presentation := presentTool("read", map[string]any{
		"path": "pkg/main.go", "offset": float64(12), "limit": float64(4),
	}, "/workspace")
	if presentation.title != "Read file" || presentation.kind != acp.ToolKindRead {
		t.Fatalf("presentation = %#v", presentation)
	}
	if len(presentation.locations) != 1 || presentation.locations[0].Path != "/workspace/pkg/main.go" ||
		presentation.locations[0].Line == nil || *presentation.locations[0].Line != 12 {
		t.Fatalf("locations = %#v", presentation.locations)
	}
	args, ok := presentation.rawInput.(map[string]any)
	if !ok || args["limit"] != float64(4) || args["offset"] != nil || args["path"] != nil {
		t.Fatalf("display input = %#v", presentation.rawInput)
	}

	update := acp.StartToolCall("read-1", presentation.title, startToolOptions(presentation, acp.ToolCallStatusPending)...)
	if _, duplicated := update.ToolCall.RawInput.(map[string]any)["path"]; duplicated {
		t.Fatalf("SDK mirrored path into input: %#v", update.ToolCall.RawInput)
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
