//go:build windows

package shell_test

import (
	"context"
	"strings"
	"syscall"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
	"golang.org/x/sys/windows"
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

func TestCommandSuppressesConsoleWindow(t *testing.T) {
	cmd := Command(context.Background(), "Write-Output ok", t.TempDir())

	if cmd.SysProcAttr == nil {
		t.Fatal("Command did not create process attributes")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("Command did not set CREATE_NO_WINDOW")
	}
	if cmd.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("Command did not set CREATE_NEW_PROCESS_GROUP")
	}
}
