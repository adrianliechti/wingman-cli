//go:build windows

package claudedesktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func supported() error {
	return nil
}

func profileRoots() (string, string, error) {
	local, err := localAppData()
	if err != nil {
		return "", "", err
	}

	return filepath.Join(local, "Claude"), filepath.Join(local, "Claude-3p"), nil
}

func isRunning() bool {
	out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", `(Get-Process claude -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1).Id`).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func openApp() error {
	path, err := windowsAppPath()
	if err != nil {
		return err
	}

	return exec.Command("powershell.exe", "-NoProfile", "-Command", "Start-Process -FilePath "+quotePowerShellString(path)).Run()
}

func killApp() error {
	script := `Get-Process claude -ErrorAction SilentlyContinue | Stop-Process -Force`
	return exec.Command("powershell.exe", "-NoProfile", "-Command", script).Run()
}

func windowsAppPath() (string, error) {
	if path := runningAppPath(); path != "" {
		return path, nil
	}

	local, err := localAppData()
	if err != nil {
		return "", err
	}

	path := filepath.Join(local, "Programs", "Claude", "Claude.exe")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("Claude Desktop executable was not found at %s; open Claude Desktop manually once and re-run the command", path)
}

func runningAppPath() string {
	script := `(Get-Process claude -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 -and $_.Path } | Select-Object -First 1 -ExpandProperty Path)`
	out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

func localAppData() (string, error) {
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return local, nil
	}

	if home := strings.TrimSpace(os.Getenv("USERPROFILE")); home != "" {
		return filepath.Join(home, "AppData", "Local"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, "AppData", "Local"), nil
}

func quotePowerShellString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
