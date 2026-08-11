package todo

import (
	"context"
	"strings"
	"testing"
)

func TestTodoReturnsSummaryNotEcho(t *testing.T) {
	tl := Tools()[0]

	result, err := tl.Execute(context.Background(), map[string]any{
		"items": []any{
			map[string]any{"content": "read the code", "status": "completed"},
			map[string]any{"content": "apply the fix", "status": "in_progress"},
			map[string]any{"content": "run the tests", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "1/3") {
		t.Errorf("result missing progress summary:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "read the code") {
		t.Errorf("result must not echo the list back (it is already in the call args):\n%s", result.Content)
	}
}

func TestTodoValidatesInput(t *testing.T) {
	tl := Tools()[0]

	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing items", map[string]any{}},
		{"invalid status", map[string]any{"items": []any{map[string]any{"content": "x", "status": "done"}}}},
		{"empty content", map[string]any{"items": []any{map[string]any{"content": " ", "status": "pending"}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tl.Execute(context.Background(), tc.args); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestTodoEmptyListClears(t *testing.T) {
	tl := Tools()[0]

	result, err := tl.Execute(context.Background(), map[string]any{"items": []any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "cleared") {
		t.Fatalf("unexpected result: %s", result.Content)
	}
}
