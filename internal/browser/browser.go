// Package browser opens URLs in the user's default web browser.
package browser

import (
	"os/exec"
	"runtime"
)

// Open launches the default browser for url without waiting for it to exit.
func Open(url string) error {
	var name string
	var args []string

	switch runtime.GOOS {
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		name, args = "open", []string{url}
	default:
		name, args = "xdg-open", []string{url}
	}

	cmd := exec.Command(name, args...)

	if err := cmd.Start(); err != nil {
		return err
	}

	go func() { _ = cmd.Wait() }()

	return nil
}
