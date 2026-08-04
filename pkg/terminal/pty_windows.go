//go:build windows

package terminal

import (
	"os"
	"os/exec"
)

const ptySupported = false

func startPTY(cmd *exec.Cmd, cols, rows int) (*os.File, error) {
	return nil, ErrUnsupported
}

func setPTYSize(f *os.File, cols, rows int) error {
	return ErrUnsupported
}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
