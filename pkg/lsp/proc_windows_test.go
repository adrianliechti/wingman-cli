//go:build windows

package lsp

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSetSysProcAttrSuppressesConsoleWindow(t *testing.T) {
	cmd := exec.Command("language-server")

	setSysProcAttr(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("setSysProcAttr did not create process attributes")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("setSysProcAttr did not set CREATE_NO_WINDOW")
	}
	if cmd.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("setSysProcAttr did not set CREATE_NEW_PROCESS_GROUP")
	}
}
