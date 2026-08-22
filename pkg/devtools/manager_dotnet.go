package devtools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var dotnetRecipes = []recipe{
	{ID: "csharp-ls", Label: "C# language tools", Kind: installerDotnet, Packages: []string{"csharp-ls"}, Commands: []string{"csharp-ls"}},
	{ID: "netcoredbg", Label: ".NET debugger", Kind: installerNetCoreDbg, Commands: []string{"netcoredbg"}},
}

const netCoreDbgReleaseURL = "https://api.github.com/repos/Samsung/netcoredbg/releases/latest"

func (m *Manager) installDotnet(ctx context.Context, item recipe, stage string) error {
	dotnet, err := m.look("dotnet")
	if err != nil {
		return errors.New("dotnet is not installed")
	}
	bin := filepath.Join(stage, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	dotnetHome := filepath.Join(stage, ".dotnet-home")
	defer os.RemoveAll(dotnetHome)
	env := append(os.Environ(),
		"DOTNET_CLI_HOME="+dotnetHome,
		"DOTNET_NOLOGO=1",
		"DOTNET_CLI_TELEMETRY_OPTOUT=1",
	)
	for _, pkg := range item.Packages {
		args := []string{"tool", "install", "--tool-path", bin, pkg}
		if output, err := m.run(ctx, dotnet, args, installWorkingDir(item, stage), env); err != nil {
			return commandError(output, err)
		}
	}
	return nil
}

func (m *Manager) installNetCoreDbg(ctx context.Context, item recipe, stage string) error {
	if item.ID != "netcoredbg" {
		return fmt.Errorf("unknown NetCoreDbg tool %q", item.ID)
	}
	assetName, err := netCoreDbgAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	archive, err := m.githubAsset(ctx, netCoreDbgReleaseURL, assetName)
	if err != nil {
		return fmt.Errorf("download NetCoreDbg: %w", err)
	}
	if strings.HasSuffix(assetName, ".zip") {
		err = extractZip(archive, stage)
	} else {
		err = extractTarGzip(archive, stage)
	}
	if err != nil {
		return fmt.Errorf("extract %s: %w", assetName, err)
	}
	adapter := filepath.Join(stage, "netcoredbg", "netcoredbg")
	if runtime.GOOS == "windows" {
		adapter += ".exe"
	}
	if !executableFile(adapter) {
		return errors.New("NetCoreDbg archive does not contain its native adapter")
	}
	return writeNetCoreDbgLauncher(stage)
}

func netCoreDbgAssetName(goos, goarch string) (string, error) {
	assets := map[string]string{
		"darwin/arm64":  "netcoredbg-osx-arm64.zip",
		"linux/amd64":   "netcoredbg-linux-amd64.tar.gz",
		"linux/arm64":   "netcoredbg-linux-arm64.tar.gz",
		"windows/amd64": "netcoredbg-win64.zip",
	}
	asset := assets[goos+"/"+goarch]
	if asset == "" {
		return "", fmt.Errorf("NetCoreDbg does not publish a release for %s/%s", goos, goarch)
	}
	return asset, nil
}

func writeNetCoreDbgLauncher(stage string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		contents := "@echo off\r\n\"%~dp0\\..\\netcoredbg\\netcoredbg.exe\" %*\r\n"
		return os.WriteFile(filepath.Join(directory, "netcoredbg.cmd"), []byte(contents), 0o755)
	}
	contents := "#!/bin/sh\nexec \"$(dirname \"$0\")/../netcoredbg/netcoredbg\" \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, "netcoredbg"), []byte(contents), 0o755)
}
