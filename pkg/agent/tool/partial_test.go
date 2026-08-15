package tool

import (
	"encoding/json"
	"testing"
)

func TestCompleteJSONPrefix(t *testing.T) {
	cases := []string{
		`{"items":[`,
		`{"items":[{"content":"Fix parser","status":"pending"}`,
		`{"items":[{"content":"Fix parser","status":"pending"},`,
		`{"items":[{"content":"Fix parser","status":"pending"},{"content":"Add te`,
		`{"items":[{"content":"Fix parser","status":`,
		`{"items":[{"content":"quote \" and \\ escape`,
		`{"items":[{"content":"trailing escape\`,
		`{"command":"go test ./...`,
	}

	for _, input := range cases {
		repaired, ok := completeJSONPrefix(input)
		if !ok {
			t.Fatalf("completeJSONPrefix(%q) failed", input)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(repaired), &out); err != nil {
			t.Fatalf("completeJSONPrefix(%q) = %q, still invalid: %v", input, repaired, err)
		}
	}
}

func TestCompleteJSONPrefixRejectsInvalidOrCompleteJSON(t *testing.T) {
	for _, input := range []string{``, `{"done":true}`, `]`, `{"a":1}]`, `{"a":[}`, `{"a":{]`} {
		if completed, ok := completeJSONPrefix(input); ok {
			t.Fatalf("completeJSONPrefix(%q) = %q, want failure", input, completed)
		}
	}
}

func TestParseTodoItemsPartial(t *testing.T) {
	partial := `{"items":[{"content":"Fix parser","status":"completed"},{"content":"Add tests","status":"in_progress"},{"content":"Upd`
	items := ParseTodoItems(partial)
	if len(items) != 3 {
		t.Fatalf("items = %v, want 3", items)
	}
	if items[0].Status != "completed" || items[1].Status != "in_progress" {
		t.Fatalf("statuses = %v", items)
	}
	if items[2].Content != "Upd" || items[2].Status != "" {
		t.Fatalf("trailing item = %+v", items[2])
	}

	empty := `{"items":[{"content":"Fix parser","status":"pending"},{"content":"`
	items = ParseTodoItems(empty)
	if len(items) != 1 || items[0].Content != "Fix parser" {
		t.Fatalf("items = %v, want only the complete entry", items)
	}
}

func TestExtractHintPartialArgs(t *testing.T) {
	if hint := ExtractHint(`{"command":"go test ./pkg/agen`, "shell"); hint != "go test ./pkg/agen" {
		t.Fatalf("hint = %q", hint)
	}
	if hint := ExtractHint(`{"file_path":"pkg/agent/cli`, "read"); hint != "/pkg/agent/cli" {
		t.Fatalf("hint = %q", hint)
	}
}
