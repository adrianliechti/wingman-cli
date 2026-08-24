//go:build windows

package process

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideCreatesProcessAttributes(t *testing.T) {
	cmd := exec.Command("cmd.exe")

	Hide(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("Hide did not create process attributes")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("Hide did not set CREATE_NO_WINDOW")
	}
}

func TestHidePreservesProcessAttributes(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	Hide(cmd)

	if cmd.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("Hide replaced existing creation flags")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("Hide did not set CREATE_NO_WINDOW")
	}
}
