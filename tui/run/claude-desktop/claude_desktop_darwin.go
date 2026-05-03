//go:build darwin

package claudedesktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func supported() error {
	return nil
}

func profileRoots() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	base := filepath.Join(home, "Library", "Application Support")
	return filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p"), nil
}

func isRunning() bool {
	out, err := exec.Command("pgrep", "-f", "Claude.app/Contents/MacOS/Claude").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func openApp() error {
	cmd := exec.Command("open", "-a", "Claude")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func killApp() error {
	return exec.Command("pkill", "-f", "Claude.app/Contents/MacOS/Claude").Run()
}
