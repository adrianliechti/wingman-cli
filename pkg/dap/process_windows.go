//go:build windows

package dap

import "os/exec"

func configureAdapterProcess(_ *exec.Cmd) {}

func killAdapterProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
