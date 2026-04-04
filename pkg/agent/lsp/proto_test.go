package lsp

import (
	"encoding/json"
	"testing"
)

func TestHoverContents_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain string",
			input: `"hello world"`,
			want:  "hello world",
		},
		{
			name:  "markup content",
			input: `{"kind": "markdown", "value": "func main()"}`,
			want:  "func main()",
		},
		{
			name:  "marked string",
			input: `{"language": "go", "value": "type Foo struct{}"}`,
			want:  "type Foo struct{}",
		},
		{
			name:  "array of strings",
			input: `["line one", "line two"]`,
			want:  "line one\nline two",
		},
		{
			name:  "array of marked strings",
			input: `[{"language": "go", "value": "func A()"}, {"language": "go", "value": "func B()"}]`,
			want:  "func A()\nfunc B()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h HoverContents
			if err := json.Unmarshal([]byte(tt.input), &h); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if h.Value != tt.want {
				t.Errorf("got %q, want %q", h.Value, tt.want)
			}
		})
	}
}

func TestPublishDiagnosticsParams_Unmarshal(t *testing.T) {
	input := `{
		"uri": "file:///test.go",
		"diagnostics": [
			{
				"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 5}},
				"severity": 1,
				"message": "error here"
			}
		]
	}`

	var params PublishDiagnosticsParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if params.URI != "file:///test.go" {
		t.Errorf("URI = %q, want file:///test.go", params.URI)
	}
	if len(params.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(params.Diagnostics))
	}
	if params.Diagnostics[0].Severity != DiagnosticSeverityError {
		t.Errorf("severity = %d, want %d", params.Diagnostics[0].Severity, DiagnosticSeverityError)
	}
}
