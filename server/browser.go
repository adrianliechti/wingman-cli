package server

import (
	"os/exec"
	"path"
	"runtime"

	"github.com/adrianliechti/wingman-agent/internal/browser"
)

func openBrowser(url string) {
	_ = browser.Open(url)
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
