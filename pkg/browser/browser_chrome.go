package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func discoverChrome() discoveredProvider {
	npx, err := exec.LookPath("npx")
	if err != nil {
		return discoveredProvider{setup: "Install a Node.js LTS release with npm/npx to enable Chrome DevTools MCP."}
	}
	if findChrome() == "" {
		return discoveredProvider{setup: "Install current stable Google Chrome to enable Chrome DevTools MCP."}
	}
	return discoveredProvider{
		available: true, command: npx, requiresDownload: true,
		args: []string{
			"-y", "chrome-devtools-mcp@latest",
			"--headless", "--isolated", "--experimentalVision",
			"--experimentalPageIdRouting", "--experimentalStructuredContent",
			"--no-usage-statistics", "--no-performance-crux",
			"--screenshot-max-width=1800", "--screenshot-max-height=1400",
		},
		setup: "The first connection may download the official chrome-devtools-mcp npm package. Chrome runs with an isolated temporary profile.",
	}
}

func findChrome() string {
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"))
		}
	case "windows":
		for _, base := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if base != "" {
				candidates = append(candidates, filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
