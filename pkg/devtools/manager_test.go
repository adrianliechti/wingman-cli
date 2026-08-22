package devtools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewUsesToolsDirectoryWithoutStoreSegment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WINGMAN_HOME", home)
	manager, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := manager.Root(), filepath.Join(home, "tools"); got != want {
		t.Fatalf("Root = %q, want %q", got, want)
	}
}

func TestUpdateSelectsPreferredManagedAlternativeAndRemovesOld(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	manager.now = func() time.Time { return time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC) }
	var installed []string
	manager.install = func(_ context.Context, item recipe, stage string) error {
		installed = append(installed, item.ID)
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return err
			}
		}
		return nil
	}

	old := filepath.Join(root, "typescript-language-server", "old.txt")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := manager.Update(context.Background(), []Requirement{
		{Alternatives: []string{"unknown", "tsc", "typescript-language-server"}},
		{Alternatives: []string{"typescript-language-server"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("Update did not report a change")
	}
	if len(installed) != 1 || installed[0] != "typescript-language-server" {
		t.Fatalf("installed = %v", installed)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old installation remains: %v", err)
	}
	if got := manager.Resolve("typescript-language-server"); got == "" {
		t.Fatal("managed command was not resolved")
	}
	status, err := os.ReadFile(filepath.Join(root, "typescript-language-server", statusName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(status))); err != nil {
		t.Fatalf("managed status = %q: %v", status, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "-old-") || strings.Contains(entry.Name(), "-install-") {
			t.Fatalf("temporary installation remains: %s", entry.Name())
		}
	}
}

func TestUpdateSkipsFreshInstallation(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	installCount := 0
	manager.install = func(_ context.Context, item recipe, stage string) error {
		installCount++
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return err
			}
		}
		return nil
	}
	requirements := []Requirement{{Alternatives: []string{"gopls"}}}

	if changed, err := manager.Update(context.Background(), requirements); err != nil || !changed {
		t.Fatalf("first Update = %v, %v", changed, err)
	}
	if changed, err := manager.Update(context.Background(), requirements); err != nil || changed {
		t.Fatalf("fresh Update = %v, %v", changed, err)
	}
	if installCount != 1 {
		t.Fatalf("install count = %d", installCount)
	}

	now = now.Add(updateInterval)
	if changed, err := manager.Update(context.Background(), requirements); err != nil || !changed {
		t.Fatalf("stale Update = %v, %v", changed, err)
	}
	if installCount != 2 {
		t.Fatalf("install count after refresh = %d", installCount)
	}
}

func TestUpdateKeepsCurrentInstallationWhenInstallerFails(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	manager.install = func(_ context.Context, item recipe, stage string) error {
		return os.ErrPermission
	}
	current := filepath.Join(root, "gopls", "bin", commandNames("gopls")[0])
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}

	changed, err := manager.Update(context.Background(), []Requirement{{Alternatives: []string{"gopls"}}})
	if err == nil || changed {
		t.Fatalf("Update = %v, %v", changed, err)
	}
	data, readErr := os.ReadFile(current)
	if readErr != nil || string(data) != "current" {
		t.Fatalf("current installation changed: %q, %v", data, readErr)
	}
}

func TestRecoverInterruptedUpdateRestoresPreviousInstallation(t *testing.T) {
	root := t.TempDir()
	backup := filepath.Join(root, ".gopls-old-backup")
	command := filepath.Join(backup, "bin", commandNames("gopls")[0])
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	staleStage := filepath.Join(root, ".gopls-install-stale")
	if err := os.MkdirAll(staleStage, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := recoverInterruptedUpdate(root, "gopls"); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(root, "gopls", "bin", commandNames("gopls")[0])
	data, err := os.ReadFile(restored)
	if err != nil || string(data) != "previous" {
		t.Fatalf("restored installation = %q, %v", data, err)
	}
	if _, err := os.Stat(staleStage); !os.IsNotExist(err) {
		t.Fatalf("stale stage remains: %v", err)
	}
}

func TestRecoverInterruptedUpdateRestoresNewestBackup(t *testing.T) {
	root := t.TempDir()
	oldTime := time.Now().Add(-time.Hour)
	newTime := oldTime.Add(30 * time.Minute)
	for name, value := range map[string]struct {
		content string
		stamp   time.Time
	}{
		".gopls-old-z": {content: "previous", stamp: oldTime},
		".gopls-old-a": {content: "newest", stamp: newTime},
	} {
		backup := filepath.Join(root, name)
		command := filepath.Join(backup, "bin", commandNames("gopls")[0])
		if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(command, []byte(value.content), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(backup, value.stamp, value.stamp); err != nil {
			t.Fatal(err)
		}
	}

	if err := recoverInterruptedUpdate(root, "gopls"); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(root, "gopls", "bin", commandNames("gopls")[0])
	data, err := os.ReadFile(command)
	if err != nil || string(data) != "newest" {
		t.Fatalf("restored installation = %q, %v", data, err)
	}
}

func TestUpdateLockReleasePreservesReplacementOwner(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, updateLockName)
	release, err := acquireUpdateLock(context.Background(), root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	release()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement lock = %q, %v", data, err)
	}
}

func TestUpdateLockRecoversLegacyStaleDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, updateLockName)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-staleLockAge - time.Minute)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	release, err := acquireUpdateLock(context.Background(), root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock remains after release: %v", err)
	}
	matches, err := filepath.Glob(path + "-stale-*")
	if err != nil || len(matches) != 0 {
		t.Fatalf("stale locks = %v, %v", matches, err)
	}
}

func TestCleanMachineCatalogIncludesRustDotnetAndJava(t *testing.T) {
	manager := newManager(t.TempDir())
	for _, command := range []string{"rust-analyzer", "codelldb", "csharp-ls", "netcoredbg", "jdtls", "js-debug-adapter"} {
		if !manager.CanManage(command) {
			t.Errorf("CanManage(%q) = false", command)
		}
	}
}

func TestJavaScriptAdapterUsesVerifiedStandaloneRelease(t *testing.T) {
	archive := testTarGzip(t, map[string]string{
		"js-debug/src/dapDebugServer.js": "console.log('adapter')\n",
		"js-debug/LICENSE":               "MIT\n",
	})
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	metadata := fmt.Sprintf(`{"assets":[{"name":"js-debug-dap-v1.2.3.tar.gz","browser_download_url":"https://github.com/microsoft/vscode-js-debug/releases/download/v1.2.3/js-debug-dap-v1.2.3.tar.gz","digest":%q}]}`, digest)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		if address == javascriptReleaseURL {
			return []byte(metadata), nil
		}
		return archive, nil
	}
	stage := t.TempDir()
	if err := manager.installRecipe(context.Background(), manager.byCommand["js-debug-adapter"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "js-debug-adapter"); got == "" {
		t.Fatal("standalone JavaScript adapter launcher was not installed")
	}
	if _, err := os.Stat(filepath.Join(stage, "js-debug", "src", "dapDebugServer.js")); err != nil {
		t.Fatal(err)
	}
}

func TestJavaScriptAdapterRejectsArchiveTraversal(t *testing.T) {
	archive := testTarGzip(t, map[string]string{"../escape": "bad"})
	if err := extractTarGzip(archive, t.TempDir()); err == nil {
		t.Fatal("archive traversal was accepted")
	}
}

func TestCodeLLDBUsesVerifiedPlatformRelease(t *testing.T) {
	adapterName := "codelldb"
	if runtime.GOOS == "windows" {
		adapterName += ".exe"
	}
	archive := testZip(t, map[string]testZipEntry{
		"extension/adapter/" + adapterName: {contents: "adapter", mode: 0o755},
		"extension/lldb/runtime":           {contents: "lldb", mode: 0o644},
	})
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	assetName, err := codeLLDBAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	metadata := fmt.Sprintf(`{"assets":[{"name":%q,"browser_download_url":"https://example.test/codelldb.vsix","digest":%q}]}`, assetName, digest)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		if address == codeLLDBReleaseURL {
			return []byte(metadata), nil
		}
		return archive, nil
	}
	stage := t.TempDir()
	if err := manager.installRecipe(context.Background(), manager.byCommand["codelldb"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "codelldb"); got == "" {
		t.Fatal("CodeLLDB launcher was not installed")
	}
	if _, err := os.Stat(filepath.Join(stage, "extension", "lldb", "runtime")); err != nil {
		t.Fatal(err)
	}
}

func TestNetCoreDbgUsesVerifiedPlatformRelease(t *testing.T) {
	adapterName := "netcoredbg"
	if runtime.GOOS == "windows" {
		adapterName += ".exe"
	}
	archive := testZip(t, map[string]testZipEntry{
		"netcoredbg/" + adapterName: {contents: "adapter", mode: 0o755},
		"netcoredbg/runtime.dll":    {contents: "runtime", mode: 0o644},
	})
	assetName, err := netCoreDbgAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	if strings.HasSuffix(assetName, ".tar.gz") {
		archive = testTarGzip(t, map[string]string{
			"netcoredbg/" + adapterName: "adapter",
			"netcoredbg/runtime.dll":    "runtime",
		})
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	metadata := fmt.Sprintf(`{"assets":[{"name":%q,"browser_download_url":"https://example.test/netcoredbg","digest":%q}]}`, assetName, digest)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		if address == netCoreDbgReleaseURL {
			return []byte(metadata), nil
		}
		return archive, nil
	}
	stage := t.TempDir()
	if err := manager.installRecipe(context.Background(), manager.byCommand["netcoredbg"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "netcoredbg"); got == "" {
		t.Fatal("NetCoreDbg launcher was not installed")
	}
}

func TestJavaInstallsLatestVerifiedJDTLSAndDebugBundle(t *testing.T) {
	jdtlsArchive := testTarGzip(t, map[string]string{
		"bin/jdtls":                "#!/bin/sh\n",
		"plugins/jdtls-plugin.jar": "jdtls",
	})
	javaDebugArchive := testZip(t, map[string]testZipEntry{
		"extension/server/com.microsoft.java.debug.plugin-0.53.2.jar": {contents: "java-debug", mode: 0o644},
	})
	jdtlsFilename := "jdt-language-server-1.60.0-build.tar.gz"
	jdtlsBase := jdtlsMilestonesURL + "1.60.0/"
	javaDebugURL := "https://example.test/java-debug.vsix"
	javaDebugSHAURL := "https://example.test/java-debug.sha256"
	javaMetadata := fmt.Sprintf(`{"verified":true,"files":{"download":%q,"sha256":%q}}`, javaDebugURL, javaDebugSHAURL)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		switch address {
		case jdtlsMilestonesURL:
			return []byte(`<a href='/jdtls/milestones/1.9.0'>old</a><a href='/jdtls/milestones/1.60.0'>latest</a>`), nil
		case jdtlsBase + "latest.txt":
			return []byte(jdtlsFilename), nil
		case jdtlsBase + jdtlsFilename:
			return jdtlsArchive, nil
		case jdtlsBase + jdtlsFilename + ".sha256":
			return []byte(fmt.Sprintf("%x", sha256.Sum256(jdtlsArchive))), nil
		case javaDebugLatestURL:
			return []byte(javaMetadata), nil
		case javaDebugURL:
			return javaDebugArchive, nil
		case javaDebugSHAURL:
			return []byte(fmt.Sprintf("%x", sha256.Sum256(javaDebugArchive))), nil
		default:
			return nil, fmt.Errorf("unexpected URL %s", address)
		}
	}
	stage := t.TempDir()
	if err := manager.installRecipe(context.Background(), manager.byCommand["jdtls"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "jdtls"); got == "" {
		t.Fatal("JDT LS launcher was not installed")
	}
	if bundles := javaDebugBundlesAt(stage); len(bundles) != 1 {
		t.Fatalf("java-debug bundles = %#v", bundles)
	}
}

func TestZipExtractorRejectsArchiveTraversal(t *testing.T) {
	archive := testZip(t, map[string]testZipEntry{"../escape": {contents: "bad", mode: 0o644}})
	if err := extractZip(archive, t.TempDir()); err == nil {
		t.Fatal("archive traversal was accepted")
	}
}

func TestRustAnalyzerPrefersCargoPackage(t *testing.T) {
	manager := newManager(t.TempDir())
	item := manager.byCommand["rust-analyzer"]
	manager.look = func(command string) (string, error) {
		if command != "cargo" {
			t.Fatalf("look command = %q", command)
		}
		return "/toolchain/cargo", nil
	}
	var gotName string
	var gotArgs []string
	manager.run = func(_ context.Context, name string, args []string, dir string, _ []string) ([]byte, error) {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil, writeTestCommand(dir, "rust-analyzer")
	}
	stage := t.TempDir()
	if err := manager.installRecipe(context.Background(), item, stage); err != nil {
		t.Fatal(err)
	}
	if gotName != "/toolchain/cargo" {
		t.Fatalf("runner = %q", gotName)
	}
	wantArgs := []string{"install", "--root", stage, "--locked", "ra_ap_rust-analyzer"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
}

func TestCSharpLanguageServerUsesDotnetTool(t *testing.T) {
	manager := newManager(t.TempDir())
	item := manager.byCommand["csharp-ls"]
	manager.look = func(command string) (string, error) {
		if command != "dotnet" {
			t.Fatalf("look command = %q", command)
		}
		return "/sdk/dotnet", nil
	}
	var gotArgs []string
	manager.run = func(_ context.Context, _ string, args []string, dir string, _ []string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return nil, writeTestCommand(dir, "csharp-ls")
	}
	stage := t.TempDir()
	if err := manager.installRecipe(context.Background(), item, stage); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"tool", "install", "--tool-path", filepath.Join(stage, "bin"), "csharp-ls"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
}

func writeTestCommand(root, command string) error {
	directory := filepath.Join(root, "bin")
	name := command
	if runtime.GOOS == "windows" {
		directory = filepath.Join(root, "Scripts")
		name += ".exe"
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, name), []byte("test"), 0o755)
}

func testTarGzip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	gzipWriter := gzip.NewWriter(&data)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, contents := range files {
		mode := int64(0o644)
		if strings.HasPrefix(name, "bin/") || strings.HasSuffix(name, "/netcoredbg") || strings.HasSuffix(name, "/netcoredbg.exe") {
			mode = 0o755
		}
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(contents))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type testZipEntry struct {
	contents string
	mode     os.FileMode
}

func testZip(t *testing.T, files map[string]testZipEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, entry := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
