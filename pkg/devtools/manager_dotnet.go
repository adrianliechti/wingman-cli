package devtools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

var dotnetRecipes = []recipe{
	{ID: "csharp-ls", Label: "C# language tools", Kind: installerDotnet, Packages: []string{"csharp-ls"}, Commands: []string{"csharp-ls"}},
}

func (m *Manager) installDotnet(ctx context.Context, item recipe, stage string) (string, error) {
	dotnet, err := m.look("dotnet")
	if err != nil {
		return "", errors.New("dotnet is not installed")
	}
	if !filepath.IsAbs(dotnet) {
		dotnet, err = filepath.Abs(dotnet)
		if err != nil {
			return "", fmt.Errorf("resolve dotnet: %w", err)
		}
	}
	tools := filepath.Join(stage, "tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		return "", err
	}
	dotnetHome := filepath.Join(stage, ".dotnet-home")
	defer os.RemoveAll(dotnetHome)
	env := append(os.Environ(),
		"DOTNET_CLI_HOME="+dotnetHome,
		"DOTNET_NOLOGO=1",
		"DOTNET_CLI_TELEMETRY_OPTOUT=1",
	)
	for _, pkg := range item.Packages {
		args := []string{"tool", "install", "--tool-path", tools, "--allow-roll-forward", pkg}
		if output, err := m.run(ctx, dotnet, args, installWorkingDir(item, stage), env); err != nil {
			return "", commandError(output, err)
		}
	}
	for _, command := range item.Commands {
		if err := writeDotnetToolLauncher(stage, command, dotnet); err != nil {
			return "", err
		}
	}
	return "", nil
}

// writeDotnetToolLauncher points the framework-dependent tool at the runtime
// of whichever dotnet is installed. Homebrew and other relocated SDKs do not
// register an install location, so the plain apphost cannot find them.
func writeDotnetToolLauncher(stage, command, dotnet string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		contents := "@echo off\r\n\"%~dp0\\..\\tools\\" + command + ".exe\" %*\r\n"
		return os.WriteFile(filepath.Join(directory, command+".cmd"), []byte(contents), 0o755)
	}
	contents := "#!/bin/sh\n" +
		"SCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\n" +
		"DOTNET_CMD=" + quotePOSIXShell(dotnet) + "\n" +
		"if [ -z \"$DOTNET_ROOT\" ]; then\n" +
		"  DOTNET_ROOT=$(\"$DOTNET_CMD\" --list-runtimes 2>/dev/null | sed -n 's/.*\\[\\(.*\\)\\/shared\\/.*/\\1/p' | head -n 1)\n" +
		"  [ -n \"$DOTNET_ROOT\" ] && export DOTNET_ROOT\n" +
		"fi\n" +
		"exec \"$SCRIPT_DIR/../tools/" + command + "\" \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, command), []byte(contents), 0o755)
}
