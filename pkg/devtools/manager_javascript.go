package devtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const javascriptReleaseURL = "https://api.github.com/repos/microsoft/vscode-js-debug/releases/latest"

const chromeForTestingReleaseURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

var javascriptRecipes = []recipe{
	{ID: "vscode-js-debug", Label: "JavaScript debugger", Kind: installerJavaScript, Commands: []string{"js-debug-adapter"}},
	{ID: "chrome-for-testing", Label: "Chrome browser", Kind: installerBrowser, Commands: []string{"chrome-for-testing"}},
}

type chromeForTestingRelease struct {
	Channels map[string]chromeForTestingChannel `json:"channels"`
}

type chromeForTestingChannel struct {
	Version   string `json:"version"`
	Downloads struct {
		Chrome []chromeForTestingArchive `json:"chrome"`
	} `json:"downloads"`
}

type chromeForTestingArchive struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

func (m *Manager) installJavaScript(ctx context.Context, item recipe, stage string) error {
	if item.ID != "vscode-js-debug" {
		return fmt.Errorf("unknown JavaScript tool %q", item.ID)
	}
	node, err := m.look("node")
	if err != nil {
		return errors.New("node is not installed")
	}
	metadata, err := m.fetch(ctx, javascriptReleaseURL)
	if err != nil {
		return fmt.Errorf("query latest vscode-js-debug release: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(metadata, &release); err != nil {
		return fmt.Errorf("decode vscode-js-debug release: %w", err)
	}
	var asset *githubAsset
	for i := range release.Assets {
		candidate := &release.Assets[i]
		if strings.HasPrefix(candidate.Name, "js-debug-dap-v") && strings.HasSuffix(candidate.Name, ".tar.gz") {
			asset = candidate
			break
		}
	}
	if asset == nil {
		return errors.New("latest vscode-js-debug release has no standalone DAP archive")
	}
	archive, err := m.fetch(ctx, asset.URL)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if err := verifySHA256(archive, asset.Digest); err != nil {
		return fmt.Errorf("verify %s: %w", asset.Name, err)
	}
	if err := extractTarGzip(archive, stage); err != nil {
		return fmt.Errorf("extract %s: %w", asset.Name, err)
	}
	server := filepath.Join(stage, "js-debug", "src", "dapDebugServer.js")
	if info, err := os.Stat(server); err != nil || info.IsDir() {
		return errors.New("standalone archive does not contain js-debug/src/dapDebugServer.js")
	}
	return writeJavaScriptLauncher(stage, node)
}

func writeJavaScriptLauncher(stage, node string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		contents := "@echo off\r\n\"" + node + "\" \"%~dp0\\..\\js-debug\\src\\dapDebugServer.js\" %*\r\n"
		return os.WriteFile(filepath.Join(directory, "js-debug-adapter.cmd"), []byte(contents), 0o755)
	}
	contents := "#!/bin/sh\nSCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec \"" + node + "\" \"$SCRIPT_DIR/../js-debug/src/dapDebugServer.js\" \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, "js-debug-adapter"), []byte(contents), 0o755)
}

func (m *Manager) installChromeForTesting(ctx context.Context, item recipe, stage string) error {
	if item.ID != "chrome-for-testing" {
		return fmt.Errorf("unknown browser tool %q", item.ID)
	}
	platform, err := chromeForTestingPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	metadata, err := m.fetch(ctx, chromeForTestingReleaseURL)
	if err != nil {
		return fmt.Errorf("query latest Chrome for Testing release: %w", err)
	}
	var release chromeForTestingRelease
	if err := json.Unmarshal(metadata, &release); err != nil {
		return fmt.Errorf("decode Chrome for Testing release: %w", err)
	}
	version, downloadURL := chromeForTestingDownload(release, platform)
	if downloadURL == "" {
		return fmt.Errorf("Chrome for Testing has no %s archive", platform)
	}
	archive, err := m.fetch(ctx, downloadURL)
	if err != nil {
		return fmt.Errorf("download Chrome for Testing %s: %w", version, err)
	}
	if err := extractZip(archive, stage); err != nil {
		return fmt.Errorf("extract Chrome for Testing %s: %w", version, err)
	}
	executable, err := chromeForTestingExecutable(stage, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := os.Chmod(executable, 0o755); err != nil {
		return fmt.Errorf("make Chrome for Testing executable: %w", err)
	}
	return writeChromeForTestingLauncher(stage, executable)
}

func chromeForTestingDownload(release chromeForTestingRelease, platform string) (string, string) {
	for _, name := range []string{"Stable", "Beta", "Dev", "Canary"} {
		channel := release.Channels[name]
		if channel.Version == "" {
			continue
		}
		for _, download := range channel.Downloads.Chrome {
			if download.Platform == platform && download.URL != "" {
				return channel.Version, download.URL
			}
		}
	}
	return "", ""
}

func chromeForTestingPlatform(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "darwin/arm64":
		return "mac-arm64", nil
	case "darwin/amd64":
		return "mac-x64", nil
	case "linux/arm64":
		return "linux-arm64", nil
	case "linux/amd64":
		return "linux64", nil
	case "windows/386":
		return "win32", nil
	case "windows/amd64", "windows/arm64":
		return "win64", nil
	default:
		return "", fmt.Errorf("Chrome for Testing is unavailable for %s/%s", goos, goarch)
	}
}

func chromeForTestingExecutable(root, goos, goarch string) (string, error) {
	platform, err := chromeForTestingPlatform(goos, goarch)
	if err != nil {
		return "", err
	}
	switch goos {
	case "darwin":
		return filepath.Join(root, "chrome-"+platform, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"), nil
	case "linux":
		return filepath.Join(root, "chrome-"+platform, "chrome"), nil
	case "windows":
		return filepath.Join(root, "chrome-"+platform, "chrome.exe"), nil
	default:
		return "", fmt.Errorf("Chrome for Testing is unavailable for %s/%s", goos, goarch)
	}
}

func writeChromeForTestingLauncher(stage, executable string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	relative, err := filepath.Rel(directory, executable)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		contents := "@echo off\r\n\"%~dp0\\" + strings.ReplaceAll(relative, "/", "\\") + "\" %*\r\n"
		return os.WriteFile(filepath.Join(directory, "chrome-for-testing.cmd"), []byte(contents), 0o755)
	}
	contents := "#!/bin/sh\nSCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec \"$SCRIPT_DIR/" + filepath.ToSlash(relative) + "\" \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, "chrome-for-testing"), []byte(contents), 0o755)
}
