//go:build windows

package process

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Hide prevents a background command from opening a console window. Existing
// process attributes are preserved so callers can add independent flags such
// as CREATE_NEW_PROCESS_GROUP.
func Hide(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
