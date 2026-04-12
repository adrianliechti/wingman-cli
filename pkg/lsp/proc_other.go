//go:build !windows

package lsp

import (
	"os/exec"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// No special attributes needed on non-Windows platforms.
}
