package fs_test

import (
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestTools(t *testing.T) {
	root, _, cleanup := createTestRoot(t)
	defer cleanup()

	tools := Tools(root, nil)

	expectedNames := []string{"read", "write", "edit", "grep", "glob", "view_image", "apply_patch"}

	if len(tools) != len(expectedNames) {
		t.Errorf("expected %d tools, got %d", len(expectedNames), len(tools))
	}

	names := make(map[string]bool)

	for _, tool := range tools {
		names[tool.Name] = true

		if tool.Description == "" {
			t.Errorf("tool %s has empty description", tool.Name)
		}

		if tool.Execute == nil && tool.ExecuteText == nil {
			t.Errorf("tool %s has no executor", tool.Name)
		}
	}

	for _, name := range expectedNames {
		if !names[name] {
			t.Errorf("missing expected tool: %s", name)
		}
	}
}
