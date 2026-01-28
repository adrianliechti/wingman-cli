package search

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func TestSearchTool(t *testing.T) {
	searchTool := SearchTool()

	if searchTool.Name != "search_online" {
		t.Errorf("expected name 'search_online', got '%s'", searchTool.Name)
	}

	if searchTool.Description == "" {
		t.Error("expected non-empty description")
	}

	if searchTool.Parameters == nil {
		t.Error("expected non-nil parameters")
	}

	if searchTool.Execute == nil {
		t.Error("expected non-nil execute function")
	}
}

func TestSearchToolMissingQuery(t *testing.T) {
	searchTool := SearchTool()

	env := &tool.Environment{}

	_, err := searchTool.Execute(context.Background(), env, map[string]any{})

	if err == nil {
		t.Error("expected error for missing query parameter")
	}
}

func TestSearchToolExecute(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	searchTool := SearchTool()

	env := &tool.Environment{}

	result, err := searchTool.Execute(context.Background(), env, map[string]any{
		"query": "golang programming",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestTools(t *testing.T) {
	tools := Tools()

	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}

	if tools[0].Name != "search_online" {
		t.Errorf("expected tool name 'search_online', got '%s'", tools[0].Name)
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello world"},
		{"hello  world", "hello world"},
		{"  hello world  ", "hello world"},
		{"hello\t\nworld", "hello world"},
		{"", ""},
		{"   ", ""},
	}

	for _, tc := range tests {
		result := normalize(tc.input)

		if result != tc.expected {
			t.Errorf("normalize(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}
