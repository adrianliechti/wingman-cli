package devtools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

var rustRecipes = []recipe{
	{ID: "rust-analyzer", Kind: installerCargo, Packages: []string{"ra_ap_rust-analyzer"}, Commands: []string{"rust-analyzer"}},
	{ID: "codelldb", Kind: installerCodeLLDB, Commands: []string{"codelldb"}},
}

const codeLLDBReleaseURL = "https://api.github.com/repos/vadimcn/codelldb/releases/latest"

func (m *Manager) installCargo(ctx context.Context, item recipe, stage string) error {
	cargo, err := m.look("cargo")
	if err != nil {
		return errors.New("cargo is not installed")
	}
	args := []string{"install", "--root", stage, "--locked"}
	args = append(args, item.Packages...)
	if output, err := m.run(ctx, cargo, args, stage, os.Environ()); err != nil {
		return commandError(output, err)
	}
	return nil
}

func (m *Manager) installCodeLLDB(ctx context.Context, item recipe, stage string) error {
	if item.ID != "codelldb" {
		return fmt.Errorf("unknown CodeLLDB tool %q", item.ID)
	}
	assetName, err := codeLLDBAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	archive, err := m.githubAsset(ctx, codeLLDBReleaseURL, assetName)
	if err != nil {
		return fmt.Errorf("download CodeLLDB: %w", err)
	}
	if err := extractZip(archive, stage); err != nil {
		return fmt.Errorf("extract %s: %w", assetName, err)
	}
	adapter := filepath.Join(stage, "extension", "adapter", "codelldb")
	if runtime.GOOS == "windows" {
		adapter += ".exe"
	}
	if !executableFile(adapter) {
		return errors.New("CodeLLDB archive does not contain its native adapter")
	}
	return writeCodeLLDBLauncher(stage)
}

func codeLLDBAssetName(goos, goarch string) (string, error) {
	platforms := map[string]string{
		"darwin/amd64":  "darwin-x64",
		"darwin/arm64":  "darwin-arm64",
		"linux/amd64":   "linux-x64",
		"linux/arm64":   "linux-arm64",
		"linux/arm":     "linux-armhf",
		"windows/amd64": "win32-x64",
	}
	platform := platforms[goos+"/"+goarch]
	if platform == "" {
		return "", fmt.Errorf("CodeLLDB does not publish a release for %s/%s", goos, goarch)
	}
	return "codelldb-" + platform + ".vsix", nil
}

func writeCodeLLDBLauncher(stage string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		contents := "@echo off\r\n\"%~dp0\\..\\extension\\adapter\\codelldb.exe\" %*\r\n"
		return os.WriteFile(filepath.Join(directory, "codelldb.cmd"), []byte(contents), 0o755)
	}
	contents := "#!/bin/sh\nexec \"$(dirname \"$0\")/../extension/adapter/codelldb\" \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, "codelldb"), []byte(contents), 0o755)
}
