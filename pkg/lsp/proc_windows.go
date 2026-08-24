//go:build windows

package lsp

import (
	"os/exec"
	"syscall"

	"github.com/adrianliechti/wingman-agent/internal/process"
)

func setSysProcAttr(cmd *exec.Cmd) {
	process.Hide(cmd)
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}
