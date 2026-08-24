//go:build !windows

package process

import "os/exec"

// Hide keeps background command setup platform-neutral.
func Hide(_ *exec.Cmd) {}
