package tool

import (
	"encoding/json"
	"testing"
)

func TestCloseJSONPrefix(t *testing.T) {
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
		closed := closeJSONPrefix(input)
		if closed == "" {
			t.Fatalf("closeJSONPrefix(%q) failed", input)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(closed), &out); err != nil {
			t.Fatalf("closeJSONPrefix(%q) = %q, still invalid: %v", input, closed, err)
		}
	}
}

func TestCloseJSONPrefixRejectsInvalidOrCompleteJSON(t *testing.T) {
	for _, input := range []string{``, `{"done":true}`, `]`, `{"a":1}]`, `{"a":[}`, `{"a":{]`} {
		if closed := closeJSONPrefix(input); closed != "" {
			t.Fatalf("closeJSONPrefix(%q) = %q, want failure", input, closed)
		}
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
