package server

import (
	"os/exec"
	"runtime"
)

// openBrowser opens url in the user's default web browser. Best-effort:
// failures (e.g. headless server, SSH session, no GUI) are silent. Uses
// cmd.Start so we don't block on a long-lived browser process.
func openBrowser(url string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// rundll32 is more reliable than `start` because it doesn't go
		// through cmd.exe argument quoting (URLs with `&` would break).
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // linux, freebsd, openbsd, netbsd, etc.
		cmd = exec.Command("xdg-open", url)
	}

	_ = cmd.Start()
}
