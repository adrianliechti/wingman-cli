package devtools

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

var rustRecipes = []recipe{
	{ID: "rust-analyzer", Label: "Rust language tools", Kind: installerRustup, Commands: []string{"rust-analyzer"}},
}

// installRustup adds rust-analyzer to the toolchain selected by the project's
// rust-toolchain file or rustup override. The managed launcher asks rustup for
// the active toolchain each time, keeping that per-project selection intact.
func (m *Manager) installRustup(ctx context.Context, item recipe, stage string) (string, error) {
	if item.ID != "rust-analyzer" {
		return "", errors.New("unsupported rustup tool")
	}
	rustup, err := m.look("rustup")
	if err != nil {
		return "", errors.New("rustup is not installed")
	}
	directories := item.WorkingDirs
	if len(directories) == 0 {
		directories = []string{stage}
	}
	toolchainDirs := make(map[string]string)
	for _, directory := range directories {
		active, runErr := m.run(ctx, rustup, []string{"show", "active-toolchain"}, directory, os.Environ())
		if runErr != nil {
			return "", commandError(active, runErr)
		}
		toolchain := activeRustToolchain(active)
		if toolchain == "" {
			return "", fmt.Errorf("rustup did not report an active toolchain for %s", directory)
		}
		if _, exists := toolchainDirs[toolchain]; !exists {
			toolchainDirs[toolchain] = directory
		}
	}
	toolchains := slices.Sorted(maps.Keys(toolchainDirs))
	versions := make([]string, 0, len(toolchains))
	for _, toolchain := range toolchains {
		directory := toolchainDirs[toolchain]
		if output, runErr := m.run(ctx, rustup, []string{"component", "add", "--toolchain", toolchain, "rust-analyzer"}, directory, os.Environ()); runErr != nil {
			return "", commandError(output, runErr)
		}
		output, runErr := m.run(ctx, rustup, []string{"run", toolchain, "rust-analyzer", "--version"}, directory, os.Environ())
		if runErr != nil {
			return "", commandError(output, runErr)
		}
		reportedVersion := strings.TrimSpace(string(output))
		if reportedVersion == "" {
			return "", fmt.Errorf("rust-analyzer did not report a version for toolchain %s", toolchain)
		}
		versions = append(versions, toolchain+"="+reportedVersion)
	}
	version := rustup + "|" + strings.Join(versions, "|")
	if version == m.installedVersion(item) {
		return "", errUpToDate
	}
	if err := writeRustupLauncher(stage, rustup); err != nil {
		return "", err
	}
	return version, nil
}

func activeRustToolchain(output []byte) string {
	lines := strings.Split(string(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		fields := strings.Fields(lines[index])
		if len(fields) == 0 || strings.HasSuffix(fields[0], ":") {
			continue
		}
		return fields[0]
	}
	return ""
}

func writeRustupLauncher(stage, rustup string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		contents := "@echo off\r\n" +
			"for /f \"tokens=1\" %%T in ('\"" + rustup + "\" show active-toolchain') do (\r\n" +
			"  \"" + rustup + "\" run %%T rust-analyzer %*\r\n" +
			"  exit /b %ERRORLEVEL%\r\n" +
			")\r\nexit /b 1\r\n"
		return os.WriteFile(filepath.Join(directory, "rust-analyzer.cmd"), []byte(contents), 0o755)
	}
	quoted := quotePOSIXShell(rustup)
	contents := "#!/bin/sh\n" +
		"toolchain=$(" + quoted + " show active-toolchain)\n" +
		"toolchain=${toolchain%% *}\n" +
		"[ -n \"$toolchain\" ] || exit 1\n" +
		"exec " + quoted + " run \"$toolchain\" rust-analyzer \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, "rust-analyzer"), []byte(contents), 0o755)
}
