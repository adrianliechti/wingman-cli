//go:build windows

package shell

import (
	"fmt"
	"os/exec"
)

func setupProcessGroup(cmd *exec.Cmd) {
	// Windows doesn't use process groups the same way Unix does
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", cmd.Process.Pid)).Run()
}