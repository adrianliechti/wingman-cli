//go:build !windows && !js && !plan9 && !wasip1

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopChildProcess(cmd *exec.Cmd) error {
	return signalChildProcessGroup(cmd, syscall.SIGTERM)
}

func killChildProcess(cmd *exec.Cmd) error {
	return signalChildProcessGroup(cmd, syscall.SIGKILL)
}

func signalChildProcessGroup(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
