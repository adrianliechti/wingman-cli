package devtools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

func TestLiveRustAnalyzerInstall(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to install managed rustup tools")
	}
	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	if _, err := manager.look("rustup"); err != nil {
		t.Skip("rustup is not installed")
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "Cargo.toml"), []byte("[package]\nname = \"wingman-live-rust\"\nversion = \"0.0.0\"\nedition = \"2024\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := manager.Update(ctx, []Requirement{{
		Alternatives: []string{"rust-analyzer"}, Workspace: project, Projects: []string{project},
		MinimumMajorVersions: map[string]int{"rust-analyzer": tooling.ProbeExecutes},
	}}); err != nil {
		t.Fatal(err)
	}
	launcher := manager.Resolve("rust-analyzer")
	if launcher == "" {
		t.Fatal("rust-analyzer launcher was not installed")
	}
	output, err := runCommand(ctx, launcher, []string{"--version"}, project, os.Environ())
	if err != nil {
		t.Fatalf("rust-analyzer --version: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "rust-analyzer") {
		t.Fatalf("unexpected version output: %s", output)
	}
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

func TestLiveJavaScriptDebuggerInstall(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to install managed GitHub tools")
	}
	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	if _, err := manager.look("node"); err != nil {
		t.Skip("node is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := manager.Update(ctx, []Requirement{{Alternatives: []string{"js-debug-adapter"}}}); err != nil {
		t.Fatal(err)
	}
	launcher := manager.Resolve("js-debug-adapter")
	if launcher == "" {
		t.Fatal("js-debug-adapter launcher was not installed")
	}
	if _, err := os.Stat(filepath.Join(manager.Root(), "vscode-js-debug", "js-debug", "src", "dapDebugServer.js")); err != nil {
		t.Fatal(err)
	}
	output, err := runCommand(ctx, launcher, []string{"--help"}, manager.root, os.Environ())
	if err != nil {
		t.Fatalf("js-debug-adapter --help: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Usage:") {
		t.Fatalf("unexpected adapter help: %s", output)
	}
}

func TestLiveCodeLLDBInstall(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to install managed GitHub tools")
	}
	if _, err := githubSpec("codelldb", runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}
	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := manager.Update(ctx, []Requirement{{Alternatives: []string{"codelldb"}}}); err != nil {
		t.Fatal(err)
	}
	launcher := manager.Resolve("codelldb")
	if launcher == "" || manager.ToolDir("codelldb") == "" {
		t.Fatal("CodeLLDB distribution was not installed")
	}
	output, err := runCommand(ctx, launcher, []string{"--help"}, manager.root, os.Environ())
	if err != nil {
		t.Fatalf("codelldb --help: %v\n%s", err, output)
	}
	if !strings.Contains(strings.ToLower(string(output)), "codelldb") {
		t.Fatalf("unexpected adapter help: %s", output)
	}
}

func TestLiveNetCoreDbgInstall(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to install managed GitHub tools")
	}
	if _, err := githubSpec("netcoredbg", runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}
	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := manager.Update(ctx, []Requirement{{Alternatives: []string{"netcoredbg"}}}); err != nil {
		t.Fatal(err)
	}
	launcher := manager.Resolve("netcoredbg")
	if launcher == "" || manager.ToolDir("netcoredbg") == "" {
		t.Fatal("NetCoreDbg distribution was not installed")
	}
	output, err := runCommand(ctx, launcher, []string{"--help"}, manager.root, os.Environ())
	if err != nil {
		t.Fatalf("netcoredbg --help: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "--interpreter=vscode") {
		t.Fatalf("unexpected adapter help: %s", output)
	}
}

func TestLiveJavaDebuggerInstall(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to install managed Maven tools")
	}
	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	if _, err := manager.look("mvn"); err != nil {
		t.Skip("Maven is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := manager.Update(ctx, []Requirement{{Alternatives: []string{"java-debug-adapter"}, Workspace: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	if manager.Resolve("java-debug-adapter") == "" {
		t.Fatal("Java debug availability marker was not installed")
	}
	root := manager.ToolDir("java-debug")
	if root == "" || len(javaDebugBundlesAt(root)) != 1 {
		t.Fatalf("managed Java debug bundle was not installed under %q", root)
	}
}

func TestLiveJDTLSInstall(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to install managed Maven tools")
	}
	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	if _, err := manager.look("mvn"); err != nil {
		t.Skip("Maven is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := manager.Update(ctx, []Requirement{{Alternatives: []string{"jdtls"}, Workspace: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	if manager.Resolve("jdtls") == "" {
		t.Fatal("JDT LS launcher was not installed")
	}
	root := manager.ToolDir("jdtls")
	if root == "" {
		t.Fatal("managed JDT LS directory was not installed")
	}
	if _, err := jdtlsProductVersion(root); err != nil {
		t.Fatal(err)
	}
}
