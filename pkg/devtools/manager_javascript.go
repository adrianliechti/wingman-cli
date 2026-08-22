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

var javascriptRecipes = []recipe{
	{ID: "vscode-js-debug", Kind: installerJavaScript, Commands: []string{"js-debug-adapter"}},
}

func (m *Manager) installJavaScript(ctx context.Context, item recipe, stage string) error {
	if item.ID != "vscode-js-debug" {
		return fmt.Errorf("unknown JavaScript tool %q", item.ID)
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
	return writeJavaScriptLauncher(stage)
}

func writeJavaScriptLauncher(stage string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		contents := "@echo off\r\nnode \"%~dp0\\..\\js-debug\\src\\dapDebugServer.js\" %*\r\n"
		return os.WriteFile(filepath.Join(directory, "js-debug-adapter.cmd"), []byte(contents), 0o755)
	}
	contents := "#!/usr/bin/env node\nrequire('../js-debug/src/dapDebugServer.js');\n"
	return os.WriteFile(filepath.Join(directory, "js-debug-adapter"), []byte(contents), 0o755)
}
