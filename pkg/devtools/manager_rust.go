package devtools

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

var rustRecipes = []recipe{
	{ID: "rust-analyzer", Label: "Rust language tools", Kind: installerRust, Commands: []string{"rust-analyzer"}},
	{ID: "codelldb", Label: "Rust debugger", Kind: installerCodeLLDB, Commands: []string{"codelldb"}},
}

const (
	rustAnalyzerReleaseURL = "https://api.github.com/repos/rust-lang/rust-analyzer/releases/latest"
	codeLLDBReleaseURL     = "https://api.github.com/repos/vadimcn/codelldb/releases/latest"
)

// installRustAnalyzer downloads the official release archive.
func (m *Manager) installRustAnalyzer(ctx context.Context, item recipe, stage string) (string, error) {
	if item.ID != "rust-analyzer" {
		return "", fmt.Errorf("unknown Rust tool %q", item.ID)
	}
	assetName, err := rustAnalyzerAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	archive, version, err := m.githubAsset(ctx, item, rustAnalyzerReleaseURL, assetName)
	if err != nil {
		return "", fmt.Errorf("download rust-analyzer: %w", err)
	}
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	name := "rust-analyzer"
	if runtime.GOOS == "windows" {
		name += ".exe"
		if err := extractZip(archive, directory); err != nil {
			return "", fmt.Errorf("extract %s: %w", assetName, err)
		}
	} else {
		reader, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return "", fmt.Errorf("decompress %s: %w", assetName, err)
		}
		writeErr := writeArchiveFile(filepath.Join(directory, name), 0o755, reader)
		closeErr := reader.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return "", fmt.Errorf("decompress %s: %w", assetName, err)
		}
	}
	if !tooling.Executable(filepath.Join(directory, name)) {
		return "", errors.New("rust-analyzer release does not contain its native server")
	}
	return version, nil
}

func rustAnalyzerAssetName(goos, goarch string) (string, error) {
	assets := map[string]string{
		"darwin/amd64":  "rust-analyzer-x86_64-apple-darwin.gz",
		"darwin/arm64":  "rust-analyzer-aarch64-apple-darwin.gz",
		"linux/amd64":   "rust-analyzer-x86_64-unknown-linux-gnu.gz",
		"linux/arm64":   "rust-analyzer-aarch64-unknown-linux-gnu.gz",
		"windows/amd64": "rust-analyzer-x86_64-pc-windows-msvc.zip",
		"windows/arm64": "rust-analyzer-aarch64-pc-windows-msvc.zip",
	}
	asset := assets[goos+"/"+goarch]
	if asset == "" {
		return "", fmt.Errorf("rust-analyzer does not publish a release for %s/%s", goos, goarch)
	}
	return asset, nil
}

func (m *Manager) installCodeLLDB(ctx context.Context, item recipe, stage string) (string, error) {
	if item.ID != "codelldb" {
		return "", fmt.Errorf("unknown CodeLLDB tool %q", item.ID)
	}
	assetName, err := codeLLDBAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	archive, version, err := m.githubAsset(ctx, item, codeLLDBReleaseURL, assetName)
	if err != nil {
		return "", fmt.Errorf("download CodeLLDB: %w", err)
	}
	if err := extractZip(archive, stage); err != nil {
		return "", fmt.Errorf("extract %s: %w", assetName, err)
	}
	adapter := filepath.Join(stage, "extension", "adapter", "codelldb")
	if runtime.GOOS == "windows" {
		adapter += ".exe"
	}
	if !tooling.Executable(adapter) {
		return "", errors.New("CodeLLDB archive does not contain its native adapter")
	}
	return version, writeCodeLLDBLauncher(stage)
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
