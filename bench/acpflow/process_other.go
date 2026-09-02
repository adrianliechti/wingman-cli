//go:build windows || js || plan9 || wasip1

package main

import (
	"errors"
	"os"
	"os/exec"
)

func configureChildProcess(_ *exec.Cmd) {}

func stopChildProcess(cmd *exec.Cmd) error {
	return killChildProcess(cmd)
}

func killChildProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
