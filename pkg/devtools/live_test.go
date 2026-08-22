package devtools

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLiveManagedArchiveDownloads(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to download managed release archives")
	}
	if _, err := codeLLDBAssetName(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}
	if _, err := netCoreDbgAssetName(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}

	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	changed, err := manager.Update(ctx, []Requirement{
		{Alternatives: []string{"codelldb"}},
		{Alternatives: []string{"netcoredbg"}},
		{Alternatives: []string{"jdtls"}},
		{Alternatives: []string{"rust-analyzer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("clean managed root was not updated")
	}
	for _, command := range []string{"codelldb", "netcoredbg", "jdtls", "rust-analyzer"} {
		if manager.Resolve(command) == "" {
			t.Errorf("%s was not installed", command)
		}
	}
	if len(javaDebugBundlesAt(manager.ToolDir("jdtls"))) != 1 {
		t.Errorf("managed java-debug bundle was not installed")
	}

	// A stale installation of an unchanged upstream release refreshes its
	// status stamp from metadata alone instead of re-downloading the archive.
	manager.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	changed, err = manager.Update(ctx, []Requirement{
		{Alternatives: []string{"codelldb"}},
		{Alternatives: []string{"netcoredbg"}},
		{Alternatives: []string{"jdtls"}},
		{Alternatives: []string{"rust-analyzer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("unchanged releases were reinstalled")
	}
	for _, item := range []recipe{byID(t, "codelldb"), byID(t, "netcoredbg"), byID(t, "jdtls"), byID(t, "rust-analyzer")} {
		if !manager.fresh(item) {
			t.Errorf("%s status stamp was not refreshed", item.ID)
		}
		if manager.installedVersion(item) == "" {
			t.Errorf("%s has no recorded upstream version", item.ID)
		}
	}
}

func byID(t *testing.T, id string) recipe {
	t.Helper()
	for _, item := range catalog {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("unknown recipe %q", id)
	return recipe{}
}

func TestLiveDotnetToolInstall(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to install managed dotnet tools")
	}
	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	if _, err := manager.look("dotnet"); err != nil {
		t.Skip("dotnet is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := manager.Update(ctx, []Requirement{{Alternatives: []string{"csharp-ls"}}}); err != nil {
		t.Fatal(err)
	}
	launcher := manager.Resolve("csharp-ls")
	if launcher == "" {
		t.Fatal("csharp-ls launcher was not installed")
	}
	probeCtx, cancelProbe := context.WithTimeout(ctx, 30*time.Second)
	defer cancelProbe()
	output, err := runCommand(probeCtx, launcher, []string{"--version"}, manager.root, os.Environ())
	if err != nil {
		t.Fatalf("csharp-ls --version: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "csharp-ls") {
		t.Fatalf("unexpected version output: %s", output)
	}
}
