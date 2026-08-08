//go:build windows

package shell_test

import (
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
)

func TestShellToolSchemaWindows(t *testing.T) {
	shellTool := Tools(`C:\`, nil, nil, nil)[0]
	if shellTool.Name != "shell" {
		t.Fatalf("tool name = %q, want shell", shellTool.Name)
	}
	if !strings.Contains(shellTool.Description, "PowerShell") {
		t.Fatalf("description should mention PowerShell, got: %s", shellTool.Description)
	}
	if shellTool.Execute == nil {
		t.Fatal("shell tool has nil Execute")
	}
}
