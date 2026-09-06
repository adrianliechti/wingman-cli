package devtools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestJavaScriptDebuggerUsesVerifiedLatestRelease(t *testing.T) {
	archive := testTarGzip(t, map[string]string{
		"js-debug/src/dapDebugServer.js": "console.log('adapter')\n",
		"js-debug/LICENSE":               "MIT\n",
	})
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	assetURL := "https://github.com/microsoft/vscode-js-debug/releases/download/v1.2.3/js-debug-dap-v1.2.3.tar.gz"
	metadata := fmt.Sprintf(`{"tag_name":"v1.2.3","assets":[{"name":"js-debug-dap-v1.2.3.tar.gz","browser_download_url":%q,"digest":%q,"size":%d}]}`, assetURL, digest, len(archive))

	manager := newManager(t.TempDir())
	node := filepath.Join(t.TempDir(), "node")
	manager.look = func(command string) (string, error) {
		if command != "node" {
			t.Fatalf("look command = %q", command)
		}
		return node, nil
	}
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		switch address {
		case javascriptReleaseURL:
			return []byte(metadata), nil
		case assetURL:
			return archive, nil
		default:
			return nil, fmt.Errorf("unexpected URL %s", address)
		}
	}

	item := manager.byCommand["js-debug-adapter"]
	stage := t.TempDir()
	version, err := manager.installRecipe(context.Background(), item, stage)
	if err != nil {
		t.Fatal(err)
	}
	wantVersion := "v1.2.3|" + digest + "|" + node
	if version != wantVersion {
		t.Fatalf("version = %q, want %q", version, wantVersion)
	}
	if command := resolveInstalledCommand(stage, "js-debug-adapter"); command == "" {
		t.Fatal("standalone JavaScript adapter launcher was not installed")
	}
	if _, err := os.Stat(filepath.Join(stage, "js-debug", "src", "dapDebugServer.js")); err != nil {
		t.Fatal(err)
	}
}

func TestJavaScriptDebuggerRejectsDigestMismatch(t *testing.T) {
	data := []byte("archive")
	if err := verifySHA256(data, "sha256:"+fmt.Sprintf("%064x", 1)); err == nil {
		t.Fatal("digest mismatch was accepted")
	}
}

func TestJavaScriptDebuggerRejectsArchiveTraversal(t *testing.T) {
	archive := testTarGzip(t, map[string]string{"../escape": "bad"})
	if err := extractTarGzip(archive, t.TempDir()); err == nil {
		t.Fatal("archive traversal was accepted")
	}
}

func TestCodeLLDBUsesVerifiedPlatformRelease(t *testing.T) {
	spec, err := githubSpec("codelldb", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	archive := testZip(t, map[string]string{
		spec.executable:                        "adapter",
		"extension/lldb/lib/bundled-lldb-file": "lldb",
	})
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	assetURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v1.2.3/%s", spec.owner, spec.repository, spec.assetName)
	metadata := githubTestMetadata("v1.2.3", spec.assetName, assetURL, digest, len(archive))

	manager := newManager(t.TempDir())
	manager.fetch = githubTestFetcher(t, spec.releaseURL, assetURL, metadata, archive)
	item := manager.byCommand[spec.command]
	stage := t.TempDir()
	version, err := manager.installRecipe(context.Background(), item, stage)
	if err != nil {
		t.Fatal(err)
	}
	if want := "v1.2.3|" + digest; version != want {
		t.Fatalf("version = %q, want %q", version, want)
	}
	if command := resolveInstalledCommand(stage, spec.command); command == "" {
		t.Fatal("CodeLLDB launcher was not installed")
	}
	if _, err := os.Stat(filepath.Join(stage, "extension", "lldb", "lib", "bundled-lldb-file")); err != nil {
		t.Fatalf("bundled LLDB was not retained: %v", err)
	}
}

func TestNetCoreDbgUsesVerifiedPlatformRelease(t *testing.T) {
	spec, err := githubSpec("netcoredbg", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	files := map[string]string{
		spec.executable:                    "adapter",
		"netcoredbg/ManagedPart.dll":       "managed",
		"netcoredbg/Debugger.Protocol.dll": "protocol",
	}
	var archive []byte
	if spec.archive == githubTarGzip {
		archive = testTarGzip(t, files)
	} else {
		archive = testZip(t, files)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	assetURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/1.2.3/%s", spec.owner, spec.repository, spec.assetName)
	metadata := githubTestMetadata("1.2.3", spec.assetName, assetURL, digest, len(archive))

	manager := newManager(t.TempDir())
	manager.fetch = githubTestFetcher(t, spec.releaseURL, assetURL, metadata, archive)
	item := manager.byCommand[spec.command]
	stage := t.TempDir()
	version, err := manager.installRecipe(context.Background(), item, stage)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1.2.3|" + digest; version != want {
		t.Fatalf("version = %q, want %q", version, want)
	}
	if command := resolveInstalledCommand(stage, spec.command); command == "" {
		t.Fatal("NetCoreDbg launcher was not installed")
	}
	if _, err := os.Stat(filepath.Join(stage, "netcoredbg", "Debugger.Protocol.dll")); err != nil {
		t.Fatalf("NetCoreDbg sibling files were not retained: %v", err)
	}
}

func TestGitHubPlatformAssetsAreExplicitlyAllowlisted(t *testing.T) {
	tests := []struct {
		id, goos, goarch, asset string
	}{
		{"codelldb", "darwin", "arm64", "codelldb-darwin-arm64.vsix"},
		{"codelldb", "darwin", "amd64", "codelldb-darwin-x64.vsix"},
		{"codelldb", "linux", "arm64", "codelldb-linux-arm64.vsix"},
		{"codelldb", "linux", "amd64", "codelldb-linux-x64.vsix"},
		{"codelldb", "linux", "arm", "codelldb-linux-armhf.vsix"},
		{"codelldb", "windows", "amd64", "codelldb-win32-x64.vsix"},
		{"netcoredbg", "darwin", "arm64", "netcoredbg-osx-arm64.zip"},
		{"netcoredbg", "linux", "arm64", "netcoredbg-linux-arm64.tar.gz"},
		{"netcoredbg", "linux", "amd64", "netcoredbg-linux-amd64.tar.gz"},
		{"netcoredbg", "windows", "amd64", "netcoredbg-win64.zip"},
	}
	for _, test := range tests {
		spec, err := githubSpec(test.id, test.goos, test.goarch)
		if err != nil || spec.assetName != test.asset {
			t.Errorf("githubSpec(%q, %q, %q) = %q, %v; want %q", test.id, test.goos, test.goarch, spec.assetName, err, test.asset)
		}
	}
	for _, test := range []struct{ id, goos, goarch string }{
		{"codelldb", "windows", "arm64"},
		{"netcoredbg", "darwin", "amd64"},
		{"netcoredbg", "linux", "arm"},
	} {
		if _, err := githubSpec(test.id, test.goos, test.goarch); err == nil {
			t.Errorf("githubSpec(%q, %q, %q) accepted an unsupported platform", test.id, test.goos, test.goarch)
		}
	}
}

func TestGitHubZipRejectsArchiveTraversal(t *testing.T) {
	archive := testZip(t, map[string]string{"../escape": "bad"})
	if err := extractZip(archive, t.TempDir()); err == nil {
		t.Fatal("ZIP traversal was accepted")
	}
}

func TestGitHubZipRejectsSymlinks(t *testing.T) {
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o777)
	file, err := archive.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("../escape")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(data.Bytes(), t.TempDir()); err == nil {
		t.Fatal("ZIP symlink was accepted")
	}
}

func TestGitHubAssetValidationRestrictsRepositoryAndDigest(t *testing.T) {
	spec, err := githubSpec("codelldb", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	asset := githubAsset{
		Name: spec.assetName,
		URL:  "https://github.com/vadimcn/codelldb/releases/download/v1.2.3/" + spec.assetName,
		Digest: "sha256:" +
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size: 1024,
	}
	if err := validateGitHubAsset(asset, spec); err != nil {
		t.Fatalf("valid official asset was rejected: %v", err)
	}

	untrusted := asset
	untrusted.URL = "https://github.com/example/codelldb/releases/download/v1.2.3/" + spec.assetName
	if err := validateGitHubAsset(untrusted, spec); err == nil {
		t.Fatal("asset from a different repository was accepted")
	}
	missingDigest := asset
	missingDigest.Digest = ""
	if err := validateGitHubAsset(missingDigest, spec); err == nil {
		t.Fatal("asset without a published SHA-256 digest was accepted")
	}
}

func TestGitHubInstallFailureIsUnavailable(t *testing.T) {
	manager := newManager(t.TempDir())
	manager.look = func(string) (string, error) { return "", errors.New("not found") }
	manager.install = func(context.Context, recipe, string) (string, error) {
		return "", errors.New("GitHub is blocked")
	}
	changed, err := manager.Update(context.Background(), []Requirement{{Alternatives: []string{"js-debug-adapter"}}})
	if changed || err == nil {
		t.Fatalf("Update = %v, %v", changed, err)
	}
	if !IsUnavailable(err) {
		t.Fatalf("missing managed adapter was not marked unavailable: %v", err)
	}
}

func testTarGzip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	for name, contents := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func testZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	for name, contents := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		file, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func githubTestMetadata(tag, name, assetURL, digest string, size int) []byte {
	return []byte(fmt.Sprintf(`{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":%q,"digest":%q,"size":%d}]}`, tag, name, assetURL, digest, size))
}

func githubTestFetcher(t *testing.T, releaseURL, assetURL string, metadata, archive []byte) fetcher {
	t.Helper()
	return func(_ context.Context, address string) ([]byte, error) {
		switch address {
		case releaseURL:
			return metadata, nil
		case assetURL:
			return archive, nil
		default:
			t.Fatalf("unexpected URL %s", address)
			return nil, nil
		}
	}
}
