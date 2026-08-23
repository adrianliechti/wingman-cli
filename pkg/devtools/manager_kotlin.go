package devtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	kotlinLSPReleaseURL     = "https://api.github.com/repos/Kotlin/kotlin-lsp/releases/latest"
	kotlinLSPLauncherMarker = "wingman-kotlin-lsp-launcher-v2"
)

var (
	kotlinRecipes = []recipe{
		{ID: "kotlin-lsp", Label: "Kotlin language tools", Kind: installerKotlinLSP, Commands: []string{"kotlin-lsp"}},
	}
	kotlinArchiveURLPattern = regexp.MustCompile(`https://download(?:-cdn)?\.jetbrains\.com/[^\s)"']+`)
)

type kotlinLSPRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
}

// installKotlinLSP installs the official platform-specific standalone archive
// linked from the latest GitHub release. The macOS .sit is a ZIP container;
// Linux uses tar+gzip and Windows uses ZIP.
func (m *Manager) installKotlinLSP(ctx context.Context, item recipe, stage string) (string, error) {
	if item.ID != "kotlin-lsp" {
		return "", fmt.Errorf("unknown Kotlin language tool %q", item.ID)
	}
	metadata, err := m.fetch(ctx, kotlinLSPReleaseURL)
	if err != nil {
		return "", fmt.Errorf("query latest Kotlin LSP release: %w", err)
	}
	var release kotlinLSPRelease
	if err := json.Unmarshal(metadata, &release); err != nil {
		return "", fmt.Errorf("decode Kotlin LSP release: %w", err)
	}
	downloadURL, err := kotlinLSPDownload(release, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	if downloadURL == m.installedVersion(item) {
		return "", errUpToDate
	}
	archive, err := m.fetch(ctx, downloadURL)
	if err != nil {
		return "", fmt.Errorf("download Kotlin LSP %s: %w", release.TagName, err)
	}
	checksum, err := m.fetch(ctx, downloadURL+".sha256")
	if err != nil {
		return "", fmt.Errorf("download Kotlin LSP checksum: %w", err)
	}
	if err := verifySHA256(archive, string(checksum)); err != nil {
		return "", fmt.Errorf("verify Kotlin LSP package: %w", err)
	}
	if err := installKotlinLSPArchive(archive, downloadURL, stage); err != nil {
		return "", fmt.Errorf("extract Kotlin LSP package: %w", err)
	}
	server := kotlinLSPServerPath(stage)
	if info, err := os.Stat(server); err != nil || info.IsDir() {
		return "", errors.New("Kotlin LSP package contains no native server")
	}
	if err := os.Chmod(server, 0o755); err != nil {
		return "", fmt.Errorf("make Kotlin LSP executable: %w", err)
	}
	if err := writeKotlinLSPLauncher(stage); err != nil {
		return "", err
	}
	return downloadURL, nil
}

func kotlinLSPDownload(release kotlinLSPRelease, goos, goarch string) (string, error) {
	version := strings.TrimPrefix(strings.TrimPrefix(release.TagName, "kotlin-lsp/"), "v")
	if version == "" {
		return "", errors.New("latest Kotlin LSP release has no version tag")
	}
	suffixes := map[string]string{
		"darwin/amd64":  ".sit",
		"darwin/arm64":  "-aarch64.sit",
		"linux/amd64":   ".tar.gz",
		"linux/arm64":   "-aarch64.tar.gz",
		"windows/amd64": ".win.zip",
		"windows/arm64": "-aarch64.win.zip",
	}
	suffix := suffixes[goos+"/"+goarch]
	if suffix == "" {
		return "", fmt.Errorf("Kotlin LSP does not publish a release for %s/%s", goos, goarch)
	}
	want := "/kotlin-server-" + version + suffix
	for _, address := range kotlinArchiveURLPattern.FindAllString(release.Body, -1) {
		if strings.HasSuffix(address, want) {
			return address, nil
		}
	}
	return "", fmt.Errorf("latest Kotlin LSP release has no standalone archive for %s/%s", goos, goarch)
}

func kotlinLSPServerPath(root string) string {
	name := "intellij-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(root, "server", "bin", name)
}

func installKotlinLSPArchive(data []byte, address, stage string) error {
	payload := filepath.Join(stage, "payload")
	var err error
	switch {
	case strings.HasSuffix(address, ".tar.gz"):
		err = extractTarGzip(data, payload)
	case strings.HasSuffix(address, ".sit"), strings.HasSuffix(address, ".zip"):
		err = extractZip(data, payload)
	default:
		err = fmt.Errorf("unsupported Kotlin LSP archive %q", filepath.Base(address))
	}
	if err != nil {
		return err
	}
	distribution, err := findKotlinLSPDistribution(payload)
	if err != nil {
		return err
	}
	destination := filepath.Join(stage, "server")
	if err := os.Rename(distribution, destination); err != nil {
		return fmt.Errorf("place Kotlin LSP distribution: %w", err)
	}
	if distribution != payload {
		if err := os.RemoveAll(payload); err != nil {
			return fmt.Errorf("clean Kotlin LSP archive: %w", err)
		}
	}
	return nil
}

func findKotlinLSPDistribution(root string) (string, error) {
	serverName := "intellij-server"
	if runtime.GOOS == "windows" {
		serverName += ".exe"
	}
	var distribution string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(entry.Name(), serverName) || !strings.EqualFold(filepath.Base(filepath.Dir(path)), "bin") {
			return nil
		}
		candidate := filepath.Dir(filepath.Dir(path))
		if distribution != "" && distribution != candidate {
			return errors.New("Kotlin LSP archive contains multiple server distributions")
		}
		distribution = candidate
		return nil
	})
	if err != nil {
		return "", err
	}
	if distribution == "" {
		return "", errors.New("Kotlin LSP archive contains no native server")
	}
	return distribution, nil
}

// writeKotlinLSPLauncher also exposes the distribution's bundled JBR through
// JAVA_HOME. The native server uses that runtime for itself, but its Gradle
// importer discovers JDKs separately and can otherwise reject a newer system
// JDK even when that JDK runs the project's Gradle version successfully.
func writeKotlinLSPLauncher(stage string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		contents := "@echo off\r\n" +
			"rem " + kotlinLSPLauncherMarker + "\r\n" +
			"set \"KOTLIN_LSP_HOME=%~dp0\\..\\server\"\r\n" +
			"if exist \"%KOTLIN_LSP_HOME%\\jbr\\bin\\java.exe\" set \"JAVA_HOME=%KOTLIN_LSP_HOME%\\jbr\"\r\n" +
			"\"%KOTLIN_LSP_HOME%\\bin\\intellij-server.exe\" %*\r\n"
		return os.WriteFile(filepath.Join(directory, "kotlin-lsp.cmd"), []byte(contents), 0o755)
	}
	contents := "#!/bin/sh\n" +
		"# " + kotlinLSPLauncherMarker + "\n" +
		"SCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\n" +
		"KOTLIN_LSP_HOME=\"$SCRIPT_DIR/../server\"\n" +
		"if [ -x \"$KOTLIN_LSP_HOME/jbr/Contents/Home/bin/java\" ]; then\n" +
		"  JAVA_HOME=\"$KOTLIN_LSP_HOME/jbr/Contents/Home\"\n" +
		"  export JAVA_HOME\n" +
		"elif [ -x \"$KOTLIN_LSP_HOME/jbr/bin/java\" ]; then\n" +
		"  JAVA_HOME=\"$KOTLIN_LSP_HOME/jbr\"\n" +
		"  export JAVA_HOME\n" +
		"fi\n" +
		"exec \"$KOTLIN_LSP_HOME/bin/intellij-server\" \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, "kotlin-lsp"), []byte(contents), 0o755)
}

func kotlinLSPLauncherReady(root string) bool {
	launcher := resolveInstalledCommand(root, "kotlin-lsp")
	contents, err := os.ReadFile(launcher)
	return err == nil && strings.Contains(string(contents), kotlinLSPLauncherMarker)
}
