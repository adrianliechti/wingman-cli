package agent

import (
	"slices"
	"testing"
)

func TestTelemetryFinishReasons(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		incomplete string
		messages   []Message
		want       []string
	}{
		{name: "completed text", status: "completed", want: []string{"stop"}},
		{
			name:     "completed tool call",
			status:   "completed",
			messages: []Message{{Content: []Content{{ToolCall: &ToolCall{Name: "read"}}}}},
			want:     []string{"tool_call"},
		},
		{name: "output limit", status: "incomplete", incomplete: "max_output_tokens", want: []string{"length"}},
		{name: "content filter", status: "incomplete", incomplete: "content_filter", want: []string{"content_filter"}},
		{name: "failed", status: "failed", want: []string{"error"}},
		{name: "still running", status: "in_progress"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := telemetryFinishReasons(tt.status, tt.incomplete, tt.messages); !slices.Equal(got, tt.want) {
				t.Fatalf("finish reasons = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTelemetryEmptyToolResponseIsSchemaComplete(t *testing.T) {
	parts, onlyToolResponses := telemetryMessageParts([]Content{{
		ToolResult: &ToolResult{ID: "call-1"},
	}})
	if !onlyToolResponses || len(parts) != 1 {
		t.Fatalf("parts = %v, only tool responses = %v", parts, onlyToolResponses)
	}

	fields := map[string]any{}
	for _, field := range parts[0].AsMap() {
		fields[string(field.Key)] = field.Value.AsInterface()
	}
	if fields["type"] != "tool_call_response" || fields["id"] != "call-1" {
		t.Fatalf("tool response fields = %#v", fields)
	}
	response, ok := fields["response"]
	if !ok || response != "" {
		t.Fatalf("required empty response = %#v (present %v)", response, ok)
	}
}
