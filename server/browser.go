package server

import (
	"os/exec"
	"path"
	"runtime"
)

func openBrowser(url string) {
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
	_ = startDetached(name, args...)
}

func revealPath(target string, isDir bool) error {
	name, args := revealCommand(runtime.GOOS, target, isDir)
	return startDetached(name, args...)
}

func revealCommand(goos, target string, isDir bool) (string, []string) {
	switch goos {
	case "windows":
		return "explorer.exe", []string{"/select," + target}
	case "darwin":
		return "open", []string{"-R", target}
	default:
		// The generic branch only ever runs on systems with slash-separated
		// paths, so trim with path rather than filepath: the latter would follow
		// the host's separator and mangle the target when goos is overridden.
		if !isDir {
			target = path.Dir(target)
		}
		return "xdg-open", []string{target}
	}
}

func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
