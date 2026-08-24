//go:build windows

package shell

import (
	"fmt"
	"os/exec"
	"syscall"

	"github.com/adrianliechti/wingman-agent/internal/process"
)

func setupProcessGroup(cmd *exec.Cmd) {
	process.Hide(cmd)
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	taskkill := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
	process.Hide(taskkill)
	return taskkill.Run()
}

func interruptProcessGroup(cmd *exec.Cmd) error {
	return fmt.Errorf("not supported on windows")
}
