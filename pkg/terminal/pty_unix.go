//go:build !windows

package terminal

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

const ptySupported = true

func startPTY(cmd *exec.Cmd, cols, rows int) (*os.File, error) {
	// pty.StartWithSize sets Setsid/Setctty, so the child is a session leader
	// and its pgid equals its pid — killProcess can signal the whole group.
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	return pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func setPTYSize(f *os.File, cols, rows int) error {
	return pty.Setsize(f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
