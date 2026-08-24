//go:build !windows

package terminal

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const ptySupported = true

// unixPTY runs a command under a creack/pty master, in its own session so the
// whole foreground process group can be signalled on close.
type unixPTY struct {
	cmd    *exec.Cmd
	master *os.File
}

func startPTY(spec CommandSpec, cols, rows int) (Terminal, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = commandEnv(spec.Env)

	// StartWithSize sets Setsid/Setctty, so the child is a session leader and its
	// pgid equals its pid — Close can signal the whole group.
	cmd.SysProcAttr = &syscall.SysProcAttr{}

	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}

	return &unixPTY{cmd: cmd, master: master}, nil
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.master.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.master.Write(b) }

func (p *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.master, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *unixPTY) ProcessID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *unixPTY) HasForegroundProcess() bool {
	if p.master == nil || p.cmd.Process == nil {
		return false
	}
	foregroundGroup, err := unix.IoctlGetInt(int(p.master.Fd()), unix.TIOCGPGRP)
	return err == nil && foregroundGroup > 0 && foregroundGroup != p.cmd.Process.Pid
}

func (p *unixPTY) Wait() error {
	return p.cmd.Wait()
}

func (p *unixPTY) Close() error {
	if p.cmd.Process != nil {
		if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
			_ = p.cmd.Process.Kill()
		}
	}
	return p.master.Close()
}
