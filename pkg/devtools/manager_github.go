package devtools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	javascriptReleaseURL = "https://api.github.com/repos/microsoft/vscode-js-debug/releases/latest"
	codeLLDBReleaseURL   = "https://api.github.com/repos/vadimcn/codelldb/releases/latest"
	netCoreDbgReleaseURL = "https://api.github.com/repos/Samsung/netcoredbg/releases/latest"
	maxGitHubResponse    = 128 << 20
	maxArchiveFiles      = 10_000
	maxExtractedBytes    = 512 << 20
)

var githubRecipes = []recipe{
	{
		ID: "vscode-js-debug", Label: "JavaScript debugger", Kind: installerGitHub,
		Commands: []string{"js-debug-adapter"},
	},
	{
		ID: "codelldb", Label: "Rust debugger", Kind: installerGitHub,
		Commands: []string{"codelldb"},
	},
	{
		ID: "netcoredbg", Label: ".NET debugger", Kind: installerGitHub,
		Commands: []string{"netcoredbg"},
	},
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type githubArchive string

const (
	githubTarGzip githubArchive = "tar.gz"
	githubZip     githubArchive = "zip"
)

type githubInstallSpec struct {
	releaseURL string
	owner      string
	repository string
	assetName  string
	archive    githubArchive
	command    string
	executable string
}

// Direct-download recipes are isolated here and verify official release
// assets before replacing an installation, just like package-manager updates.
func (m *Manager) installGitHub(ctx context.Context, item recipe, stage string) (string, error) {
	spec, err := githubSpec(item.ID, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	metadata, err := m.fetch(ctx, spec.releaseURL)
	if err != nil {
		return "", fmt.Errorf("query latest %s release: %w", item.Label, err)
	}
	var release githubRelease
	if err := json.Unmarshal(metadata, &release); err != nil {
		return "", fmt.Errorf("decode latest %s release: %w", item.Label, err)
	}
	asset, err := githubReleaseAsset(release, spec)
	if err != nil {
		return "", err
	}
	if err := validateGitHubAsset(*asset, spec); err != nil {
		return "", err
	}
	version := strings.TrimSpace(release.TagName) + "|" + strings.ToLower(strings.TrimSpace(asset.Digest))
	if item.ID == "vscode-js-debug" {
		node, lookErr := m.look("node")
		if lookErr != nil {
			return "", errors.New("node is not installed")
		}
		version += "|" + node
	}
	if version == m.installedVersion(item) {
		return "", errUpToDate
	}

	archive, err := m.fetch(ctx, asset.URL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if asset.Size > 0 && int64(len(archive)) != asset.Size {
		return "", fmt.Errorf("download %s: got %d bytes, want %d", asset.Name, len(archive), asset.Size)
	}
	if err := verifySHA256(archive, asset.Digest); err != nil {
		return "", fmt.Errorf("verify %s: %w", asset.Name, err)
	}
	if err := extractGitHubArchive(archive, stage, spec.archive); err != nil {
		return "", fmt.Errorf("extract %s: %w", asset.Name, err)
	}
	if err := finishGitHubInstall(m, item.ID, stage, spec); err != nil {
		return "", err
	}
	return version, nil
}

func githubSpec(id, goos, goarch string) (githubInstallSpec, error) {
	switch id {
	case "vscode-js-debug":
		return githubInstallSpec{
			releaseURL: javascriptReleaseURL, owner: "microsoft", repository: "vscode-js-debug",
			archive: githubTarGzip, command: "js-debug-adapter", executable: "js-debug/src/dapDebugServer.js",
		}, nil
	case "codelldb":
		asset, executable := codeLLDBPlatform(goos, goarch)
		if asset == "" {
			return githubInstallSpec{}, fmt.Errorf("CodeLLDB has no supported release for %s/%s", goos, goarch)
		}
		return githubInstallSpec{
			releaseURL: codeLLDBReleaseURL, owner: "vadimcn", repository: "codelldb", assetName: asset,
			archive: githubZip, command: "codelldb", executable: executable,
		}, nil
	case "netcoredbg":
		asset, archive, executable := netCoreDbgPlatform(goos, goarch)
		if asset == "" {
			return githubInstallSpec{}, fmt.Errorf("NetCoreDbg has no supported release for %s/%s", goos, goarch)
		}
		return githubInstallSpec{
			releaseURL: netCoreDbgReleaseURL, owner: "Samsung", repository: "netcoredbg", assetName: asset,
			archive: archive, command: "netcoredbg", executable: executable,
		}, nil
	default:
		return githubInstallSpec{}, fmt.Errorf("unknown GitHub tool %q", id)
	}
}

func codeLLDBPlatform(goos, goarch string) (asset, executable string) {
	platform := ""
	switch goos + "/" + goarch {
	case "darwin/amd64":
		platform = "darwin-x64"
	case "darwin/arm64":
		platform = "darwin-arm64"
	case "linux/amd64":
		platform = "linux-x64"
	case "linux/arm64":
		platform = "linux-arm64"
	case "linux/arm":
		platform = "linux-armhf"
	case "windows/amd64":
		platform = "win32-x64"
	default:
		return "", ""
	}
	executable = "extension/adapter/codelldb"
	if goos == "windows" {
		executable += ".exe"
	}
	return "codelldb-" + platform + ".vsix", executable
}

func netCoreDbgPlatform(goos, goarch string) (asset string, archive githubArchive, executable string) {
	executable = "netcoredbg/netcoredbg"
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "netcoredbg-linux-amd64.tar.gz", githubTarGzip, executable
	case "linux/arm64":
		return "netcoredbg-linux-arm64.tar.gz", githubTarGzip, executable
	case "darwin/arm64":
		return "netcoredbg-osx-arm64.zip", githubZip, executable
	case "windows/amd64":
		return "netcoredbg-win64.zip", githubZip, executable + ".exe"
	default:
		return "", "", ""
	}
}

func githubReleaseAsset(release githubRelease, spec githubInstallSpec) (*githubAsset, error) {
	if spec.assetName == "" {
		return javascriptDebugAsset(release.Assets)
	}
	var selected *githubAsset
	for index := range release.Assets {
		if release.Assets[index].Name != spec.assetName {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("latest %s/%s release has multiple %s assets", spec.owner, spec.repository, spec.assetName)
		}
		selected = &release.Assets[index]
	}
	if selected == nil {
		return nil, fmt.Errorf("latest %s/%s release has no %s asset", spec.owner, spec.repository, spec.assetName)
	}
	return selected, nil
}

func javascriptDebugAsset(assets []githubAsset) (*githubAsset, error) {
	var selected *githubAsset
	for index := range assets {
		asset := &assets[index]
		if !strings.HasPrefix(asset.Name, "js-debug-dap-v") || !strings.HasSuffix(asset.Name, ".tar.gz") {
			continue
		}
		if selected != nil {
			return nil, errors.New("latest vscode-js-debug release has multiple standalone DAP archives")
		}
		selected = asset
	}
	if selected == nil {
		return nil, errors.New("latest vscode-js-debug release has no standalone DAP archive")
	}
	return selected, nil
}

func validateGitHubAsset(asset githubAsset, spec githubInstallSpec) error {
	parsed, err := url.Parse(asset.URL)
	wantPrefix := "/" + spec.owner + "/" + spec.repository + "/releases/download/"
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") ||
		!strings.HasPrefix(parsed.Path, wantPrefix) || !strings.HasSuffix(parsed.Path, "/"+asset.Name) {
		return fmt.Errorf("latest %s/%s release has an unexpected asset URL %q", spec.owner, spec.repository, asset.URL)
	}
	if asset.Size <= 0 || asset.Size > maxGitHubResponse {
		return fmt.Errorf("latest %s/%s archive has invalid size %d", spec.owner, spec.repository, asset.Size)
	}
	if err := validateSHA256(asset.Digest); err != nil {
		return fmt.Errorf("latest %s/%s archive: %w", spec.owner, spec.repository, err)
	}
	return nil
}

func finishGitHubInstall(m *Manager, id, stage string, spec githubInstallSpec) error {
	executable := filepath.Join(stage, filepath.FromSlash(spec.executable))
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("archive does not contain %s", spec.executable)
	}

	switch id {
	case "vscode-js-debug":
		node, lookErr := m.look("node")
		if lookErr != nil {
			return errors.New("node is not installed")
		}
		return writeJavaScriptLauncher(stage, node)
	case "codelldb":
		if info, statErr := os.Stat(filepath.Join(stage, "extension", "lldb")); statErr != nil || !info.IsDir() {
			return errors.New("CodeLLDB archive does not contain its bundled LLDB distribution")
		}
	case "netcoredbg":
		managedPart := filepath.Join(stage, "netcoredbg", "ManagedPart.dll")
		if info, statErr := os.Stat(managedPart); statErr != nil || !info.Mode().IsRegular() {
			return errors.New("NetCoreDbg archive does not contain ManagedPart.dll")
		}
	default:
		return fmt.Errorf("unknown GitHub tool %q", id)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(executable, info.Mode().Perm()|0o755); err != nil {
			return fmt.Errorf("make %s executable: %w", spec.executable, err)
		}
	}
	return writeBundledLauncher(stage, spec.command, spec.executable)
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
	contents := "#!/bin/sh\nSCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec " + quotePOSIXShell(node) + " \"$SCRIPT_DIR/../js-debug/src/dapDebugServer.js\" \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, "js-debug-adapter"), []byte(contents), 0o755)
}

func writeBundledLauncher(stage, command, executable string) error {
	directory := filepath.Join(stage, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		target := strings.ReplaceAll(executable, "/", "\\")
		contents := "@echo off\r\n\"%~dp0\\..\\" + target + "\" %*\r\n"
		return os.WriteFile(filepath.Join(directory, command+".cmd"), []byte(contents), 0o755)
	}
	contents := "#!/bin/sh\nSCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec \"$SCRIPT_DIR/../" + executable + "\" \"$@\"\n"
	return os.WriteFile(filepath.Join(directory, command), []byte(contents), 0o755)
}

var githubDownloadClient = &http.Client{
	Timeout: 90 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	},
}

func fetchURL(ctx context.Context, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "wingman-agent")
	if request.URL.Hostname() == "api.github.com" {
		if token := cmp.Or(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN")); token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
	}
	response, err := githubDownloadClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	if response.ContentLength > maxGitHubResponse {
		return nil, fmt.Errorf("response is too large: %d bytes", response.ContentLength)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxGitHubResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxGitHubResponse {
		return nil, fmt.Errorf("response exceeds %d bytes", maxGitHubResponse)
	}
	return data, nil
}

func validateSHA256(published string) error {
	want := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(published)), "sha256:")
	if len(want) != sha256.Size*2 {
		return fmt.Errorf("missing or invalid SHA-256 digest %q", published)
	}
	for _, character := range want {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return fmt.Errorf("missing or invalid SHA-256 digest %q", published)
		}
	}
	return nil
}

func verifySHA256(data []byte, published string) error {
	if err := validateSHA256(published); err != nil {
		return err
	}
	want := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(published)), "sha256:")
	got := fmt.Sprintf("%x", sha256.Sum256(data))
	if got != want {
		return fmt.Errorf("SHA-256 mismatch: got %s, want %s", got, want)
	}
	return nil
}

func extractGitHubArchive(data []byte, destination string, format githubArchive) error {
	switch format {
	case githubTarGzip:
		return extractTarGzip(data, destination)
	case githubZip:
		return extractZip(data, destination)
	default:
		return fmt.Errorf("unsupported GitHub archive format %q", format)
	}
}

func extractZip(data []byte, destination string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(reader.File) > maxArchiveFiles {
		return errors.New("archive contains too many entries")
	}
	var extracted uint64
	for _, file := range reader.File {
		target, err := archiveTarget(destination, file.Name)
		if err != nil {
			return err
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return fmt.Errorf("unsupported archive entry %q", file.Name)
		}
		if mode.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if file.UncompressedSize64 > maxExtractedBytes || extracted > maxExtractedBytes-file.UncompressedSize64 {
			return errors.New("archive expands beyond the allowed size")
		}
		extracted += file.UncompressedSize64
		source, err := file.Open()
		if err != nil {
			return err
		}
		writeErr := writeArchiveFile(target, mode.Perm(), io.LimitReader(source, int64(file.UncompressedSize64)))
		closeErr := source.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}

func extractTarGzip(data []byte, destination string) error {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer compressed.Close()

	reader := tar.NewReader(compressed)
	files := 0
	var extracted int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		files++
		if files > maxArchiveFiles {
			return errors.New("archive contains too many entries")
		}
		target, err := archiveTarget(destination, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || extracted+header.Size > maxExtractedBytes {
				return errors.New("archive expands beyond the allowed size")
			}
			extracted += header.Size
			if err := writeArchiveFile(target, header.FileInfo().Mode().Perm(), io.LimitReader(reader, header.Size)); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader:
			// git-archive metadata carries no file content.
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
}

func archiveTarget(destination, name string) (string, error) {
	name = filepath.Clean(filepath.FromSlash(name))
	if name == "." {
		return destination, nil
	}
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path escapes destination: %q", name)
	}
	return filepath.Join(destination, name), nil
}

func writeArchiveFile(path string, mode os.FileMode, source io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	return errors.Join(copyErr, closeErr)
}
