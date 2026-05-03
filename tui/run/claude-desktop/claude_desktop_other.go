//go:build !darwin && !windows

package claudedesktop

import "fmt"

func supported() error {
	return fmt.Errorf("Claude Desktop launch is only supported on macOS and Windows")
}

func profileRoots() (string, string, error) {
	return "", "", supported()
}

func isRunning() bool {
	return false
}

func openApp() error {
	return supported()
}

func killApp() error {
	return supported()
}
