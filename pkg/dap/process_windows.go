//go:build windows

package dap

import (
	"os/exec"

	"github.com/adrianliechti/wingman-agent/internal/process"
)

func configureAdapterProcess(cmd *exec.Cmd) {
	process.Hide(cmd)
}

func killAdapterProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
