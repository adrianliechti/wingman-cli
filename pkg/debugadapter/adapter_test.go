package debugadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestGoAdapterPlansSingleTestDeterministically(t *testing.T) {
	plan, err := NewRegistry().Plan("Go", Request{
		Action:     "debug",
		ProjectDir: "services/api",
		Target: Target{
			Name: "TestHTTP_200", Kind: "test", Language: "Go",
			Path: "services/api/http_test.go", Directory: "services/api", Line: 18, Column: 6,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProjectDir != "services/api" || plan.Configuration["mode"] != "test" || plan.Configuration["program"] != "." || !plan.SupportsTerminal {
		t.Fatalf("plan = %#v", plan)
	}
	wantArgs := []string{"-test.run", `^TestHTTP_200$`}
	if !reflect.DeepEqual(plan.Configuration["args"], wantArgs) {
		t.Fatalf("args = %#v, want %#v", plan.Configuration["args"], wantArgs)
	}
	if len(plan.Breakpoints) != 1 || plan.Breakpoints[0].Line != 18 {
		t.Fatalf("breakpoints = %#v", plan.Breakpoints)
	}
}

func TestGoAdapterMapsOutputAndTerminalModesToDelve(t *testing.T) {
	descriptor := (goAdapter{}).Descriptor()
	if descriptor.IOConfigKey != "outputMode" || descriptor.IOValues["output"] != "remote" || descriptor.IOValues["terminal"] != "local" {
		t.Fatalf("Go I/O descriptor = %#v", descriptor)
	}
}

func TestPythonAdapterPlansWorkspaceRelativeScript(t *testing.T) {
	plan, err := NewRegistry().Plan("Python", Request{
		Action:     "run",
		ProjectDir: "services/api",
		Target: Target{
			Name: "main.py", Kind: "script", Language: "Python",
			Path: "services/api/main.py", Directory: "services/api", Line: 1, Column: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Configuration["program"] != "main.py" || plan.Configuration["noDebug"] != true || len(plan.Breakpoints) != 0 {
		t.Fatalf("plan = %#v", plan)
	}
	if _, forced := plan.Configuration["redirectOutput"]; forced {
		t.Fatalf("Python plan overrides debugpy's console-specific output policy: %#v", plan.Configuration)
	}
}

func TestPythonAdapterUsesProjectVirtualEnvironment(t *testing.T) {
	project := t.TempDir()
	bin := "bin"
	name := "python"
	if runtime.GOOS == "windows" {
		bin = "Scripts"
		name = "python.exe"
	}
	interpreter := filepath.Join(project, ".venv", bin, name)
	if err := os.MkdirAll(filepath.Dir(interpreter), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(interpreter, []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := NewRegistry().Plan("Python", Request{
		Action: "debug", WorkspaceDir: project, ProjectDir: ".",
		Target: Target{Name: "main.py", Kind: "script", Language: "Python", Path: "main.py", Directory: ".", Line: 1, Column: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{interpreter}
	if got := plan.Configuration["python"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("python = %#v, want %#v", got, want)
	}
}

func TestPythonAdapterWalksUpToWorkspaceVirtualEnvironment(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := "bin"
	name := "python"
	if runtime.GOOS == "windows" {
		bin = "Scripts"
		name = "python.exe"
	}
	interpreter := filepath.Join(root, ".venv", bin, name)
	if err := os.MkdirAll(filepath.Dir(interpreter), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(interpreter, []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := NewRegistry().Plan("Python", Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: filepath.Join("services", "api"),
		Target: Target{Name: "main.py", Kind: "script", Language: "Python", Path: filepath.Join("services", "api", "main.py"), Directory: filepath.Join("services", "api"), Line: 1, Column: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Configuration["python"]; !reflect.DeepEqual(got, []string{interpreter}) {
		t.Fatalf("python = %#v, want workspace virtual environment", got)
	}
}

func TestRustAdapterPlansConcreteCargoBinary(t *testing.T) {
	descriptor := (rustAdapter{}).Descriptor()
	if descriptor.Transport != "stdio" || len(descriptor.Args) != 0 || descriptor.ReadyPrefix != "" {
		t.Fatalf("CodeLLDB descriptor = %#v", descriptor)
	}

	root := t.TempDir()
	project := filepath.Join(root, "rust-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistryWith(rustAdapter{loadMetadata: func(projectDir string) (cargoMetadata, error) {
		if !sameCargoPath(projectDir, project) {
			t.Fatalf("metadata project = %q, want %q", projectDir, project)
		}
		return cargoMetadata{
			TargetDirectory: filepath.Join(project, "target"),
			Packages: []cargoMetadataPackage{{Targets: []cargoMetadataTarget{{
				Name: "sample-app", Kind: []string{"bin"}, SrcPath: filepath.Join(project, "src", "main.rs"),
			}}}},
		}, nil
	}})
	plan, err := registry.Plan("Rust", Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "rust-app",
		Target: Target{Name: "main", Kind: "main", Language: "Rust", Path: "rust-app/src/main.rs", Line: 1, Column: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	executable := "sample-app"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	wantProgram := filepath.ToSlash(filepath.Join("target", "debug", executable))
	if plan.Configuration["program"] != wantProgram || !strings.Contains(plan.Summary, "cargo build --bin sample-app") || !plan.SupportsTerminal {
		t.Fatalf("unbuilt plan = %#v", plan)
	}
	if _, leakedExtensionConfig := plan.Configuration["cargo"]; leakedExtensionConfig {
		t.Fatalf("plan sent CodeLLDB's extension-only cargo configuration: %#v", plan.Configuration)
	}
	if len(plan.Breakpoints) != 1 {
		t.Fatalf("breakpoints = %#v", plan.Breakpoints)
	}

	built := filepath.Join(project, filepath.FromSlash(wantProgram))
	if err := os.MkdirAll(filepath.Dir(built), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err = registry.Plan("Rust", Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "rust-app",
		Target: Target{Name: "main", Kind: "main", Language: "Rust", Path: "rust-app/src/main.rs", Line: 1, Column: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Summary, "cargo build") {
		t.Fatalf("built plan still asks for a build: %#v", plan)
	}
}

func TestRustAdapterResolvesCargoTargetsFromMetadata(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "rust-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistryWith(rustAdapter{loadMetadata: func(string) (cargoMetadata, error) {
		return cargoMetadata{
			TargetDirectory: filepath.Join(project, "target"),
			Packages: []cargoMetadataPackage{{Targets: []cargoMetadataTarget{
				{Name: "worker", Kind: []string{"bin"}, SrcPath: filepath.Join(project, "tools", "worker.rs")},
				{Name: "tour", Kind: []string{"example"}, SrcPath: filepath.Join(project, "showcase", "tour.rs")},
			}}},
		}, nil
	}})

	for _, test := range []struct {
		path        string
		programPath string
		buildHint   string
	}{
		{path: "tools/worker.rs", programPath: filepath.Join("target", "debug", "worker"), buildHint: "cargo build --bin worker"},
		{path: "showcase/tour.rs", programPath: filepath.Join("target", "debug", "examples", "tour"), buildHint: "cargo build --example tour"},
	} {
		plan, err := registry.Plan("Rust", Request{
			Action: "debug", WorkspaceDir: root, ProjectDir: "rust-app",
			Target: Target{Name: "entry", Kind: "main", Language: "Rust", Path: filepath.ToSlash(filepath.Join("rust-app", test.path)), Line: 1, Column: 4},
		})
		if err != nil {
			t.Fatal(err)
		}
		wantProgram := filepath.ToSlash(test.programPath)
		if runtime.GOOS == "windows" {
			wantProgram += ".exe"
		}
		if plan.Configuration["program"] != wantProgram || !strings.Contains(plan.Summary, test.buildHint) {
			t.Errorf("%s plan = %#v", test.path, plan)
		}
	}
}

func TestRustAdapterUsesCargoMetadataTargetDirectory(t *testing.T) {
	if resolveCargoExecutable() == "" {
		t.Skip("cargo is not installed")
	}
	root := t.TempDir()
	project := filepath.Join(root, "rust-app")
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Cargo.toml"), []byte("[package]\nname = \"sample-app\"\nversion = \"0.0.0\"\nedition = \"2024\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "main.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".cargo", "config.toml"), []byte("[build]\ntarget-dir = \"artifacts\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	metadata, err := loadCargoMetadata(project)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistryWith(rustAdapter{loadMetadata: func(string) (cargoMetadata, error) {
		return metadata, nil
	}})
	plan, err := registry.Plan("Rust", Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "rust-app",
		Target: Target{Name: "main", Kind: "main", Language: "Rust", Path: "rust-app/src/main.rs", Line: 1, Column: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(filepath.Join("artifacts", "debug", "sample-app"))
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if plan.Configuration["program"] != want {
		t.Fatalf("Cargo target directory %q produced program %#v, want %q", metadata.TargetDirectory, plan.Configuration["program"], want)
	}
}

func TestCargoMetadataAddsCargoDirectoryToPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses POSIX scripts")
	}
	bin := t.TempDir()
	cargo := filepath.Join(bin, "cargo")
	helper := filepath.Join(bin, "wingman-cargo-metadata-helper")
	if err := os.WriteFile(cargo, []byte("#!/bin/sh\nexec wingman-cargo-metadata-helper\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	helperContents := "#!/bin/sh\nprintf '{\"packages\":[],\"target_directory\":\"%s/target\"}\\n' \"$PWD\"\n"
	if err := os.WriteFile(helper, []byte(helperContents), 0o755); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "Cargo.toml"), []byte("[package]\nname = \"sample\"\nversion = \"0.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO", cargo)
	t.Setenv("PATH", "/usr/bin:/bin")
	metadata, err := loadCargoMetadata(project)
	if err != nil {
		t.Fatal(err)
	}
	canonicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalProject, "target")
	if metadata.TargetDirectory != want {
		t.Fatalf("target directory = %q, want %q", metadata.TargetDirectory, want)
	}
}

func TestDotnetAdapterPlansExistingOrExpectedAssembly(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "dotnet-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	projectFile := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework><AssemblyName>Demo</AssemblyName></PropertyGroup></Project>`
	if err := os.WriteFile(filepath.Join(project, "Demo.csproj"), []byte(projectFile), 0o644); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "dotnet-app",
		Target: Target{Name: "Program", Kind: "main", Language: dotnetLanguage, Path: "dotnet-app/Program.cs", Line: 1, Column: 1},
	}
	plan, err := NewRegistry().Plan(dotnetLanguage, request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Configuration["program"] != filepath.ToSlash(filepath.Join("bin", "Debug", "net8.0", "Demo.dll")) || !strings.Contains(plan.Summary, "dotnet build") {
		t.Fatalf("unbuilt plan = %#v", plan)
	}
	if plan.SupportsTerminal {
		t.Fatalf("NetCoreDbg plan advertises unsupported runInTerminal: %#v", plan)
	}
	descriptor := (dotnetAdapter{}).Descriptor()
	if descriptor.TerminalStrategy != "" || descriptor.IOConfigKey != "" {
		t.Fatalf("NetCoreDbg descriptor advertises unsupported terminal configuration: %#v", descriptor)
	}

	stale := filepath.Join(project, "bin", "Debug", "net7.0", "Demo.dll")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale assembly"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err = NewRegistry().Plan(dotnetLanguage, request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Configuration["program"] != filepath.ToSlash(filepath.Join("bin", "Debug", "net8.0", "Demo.dll")) || !strings.Contains(plan.Summary, "dotnet build") {
		t.Fatalf("plan selected a stale target framework assembly: %#v", plan)
	}

	built := filepath.Join(project, "bin", "Debug", "net8.0", "Demo.dll")
	if err := os.MkdirAll(filepath.Dir(built), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("assembly"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err = NewRegistry().Plan(dotnetLanguage, request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Summary, "dotnet build") {
		t.Fatalf("built plan still asks for a build: %#v", plan)
	}
	if plan.Configuration["program"] != filepath.ToSlash(filepath.Join("bin", "Debug", "net8.0", "Demo.dll")) {
		t.Fatalf("plan selected a stale target framework assembly: %#v", plan)
	}
}

func TestPackageScriptPlansViteServerAndManagedBrowser(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "web")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "vite.config.ts"), []byte("export default { server: { port: 4173 } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	npm := filepath.Join(bin, "npm")
	if runtime.GOOS == "windows" {
		npm += ".cmd"
	}
	if err := os.WriteFile(npm, []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	browser := filepath.Join(root, "chrome-for-testing")
	if err := os.WriteFile(browser, []byte("browser"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := NewRegistry().Plan(javascriptLanguage, Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "web", BrowserExecutable: browser,
		Target: Target{Name: "dev", Kind: "browser-script", Language: javascriptLanguage, Path: "web/package.json", Line: 1, Column: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Configuration["type"] != "pwa-chrome" || plan.Configuration["url"] != "http://localhost:4173" || plan.Configuration["runtimeExecutable"] != browser || plan.Configuration["server"] != nil || plan.PreLaunch == nil || plan.PreLaunch.Command != npm || !reflect.DeepEqual(plan.PreLaunch.Args, []string{"run", "dev"}) || plan.PreLaunch.ReadyURL != "http://localhost:4173" || len(plan.Breakpoints) != 0 || plan.SupportsTerminal {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPackageScriptPlansNodeServerWithoutBrowser(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"scripts":{"server":"tsx src/server.ts"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	npm := filepath.Join(bin, "npm")
	if runtime.GOOS == "windows" {
		npm += ".cmd"
	}
	if err := os.WriteFile(npm, []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	plan, err := NewRegistry().Plan(javascriptLanguage, Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "api",
		Target: Target{Name: "server", Kind: "node-script", Language: javascriptLanguage, Path: "api/package.json", Line: 1, Column: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Configuration["type"] != "pwa-node" || plan.Configuration["runtimeExecutable"] != npm || !reflect.DeepEqual(plan.Configuration["runtimeArgs"], []string{"run", "server"}) || plan.Configuration["autoAttachChildProcesses"] != true || plan.PreLaunch != nil || !plan.SupportsTerminal {
		t.Fatalf("plan = %#v", plan)
	}
	environment, ok := plan.Configuration["env"].(map[string]string)
	if !ok || environment["PATH"] != bin {
		t.Fatalf("runtime environment = %#v, want PATH %q", plan.Configuration["env"], bin)
	}
}

func TestJavaAdapterPlansMavenProjectName(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "java-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	pom := `<project><parent><artifactId>parent-name</artifactId></parent><artifactId>app-name</artifactId></project>`
	if err := os.WriteFile(filepath.Join(project, "pom.xml"), []byte(pom), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := NewRegistry().Plan("Java", Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "java-app",
		Target: Target{Name: "demo.App", Kind: "main", Language: "Java", Path: "java-app/src/main/java/demo/App.java", Line: 3, Column: 24},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Configuration["projectName"] != "app-name" {
		t.Fatalf("projectName = %#v, want app-name", plan.Configuration["projectName"])
	}
}

func TestRegistryUsesManagedJavaDebugBundleOverExternalOverride(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "com.microsoft.java.debug.plugin-test.jar")
	if err := os.WriteFile(bundle, []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINGMAN_JAVA_DEBUG_BUNDLE", bundle)
	managed := filepath.Join(root, "managed", "java-debug")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "com.microsoft.java.debug.plugin-managed.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(toolDirectoryStub{"java-debug": filepath.Dir(managed)})
	want := map[string]any{"bundles": []string{filepath.Join(managed, "com.microsoft.java.debug.plugin-managed.jar")}}
	if got := registry.ServerInitializations()["jdtls"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("jdtls initialization = %#v", got)
	}
	found := false
	for _, descriptor := range registry.Descriptors() {
		if descriptor.Name == "java-debug" {
			found = descriptor.Command == "java-debug-adapter" && descriptor.Transport == "connect"
		}
	}
	if !found {
		t.Fatal("installed java-debug bundle did not enable the Java descriptor")
	}
}

type toolDirectoryStub map[string]string

func (stub toolDirectoryStub) ToolDir(id string) string { return stub[id] }

func TestRegistryRequestsAndLoadsManagedJavaDebugger(t *testing.T) {
	t.Setenv("WINGMAN_JAVA_DEBUG_BUNDLE", "")
	root := t.TempDir()

	missing := NewRegistry(toolDirectoryStub{})
	foundRequirement := false
	for _, descriptor := range missing.Descriptors() {
		if descriptor.Name == "java-debug" {
			foundRequirement = descriptor.Command == "java-debug-adapter" && descriptor.Transport == "connect"
		}
	}
	if !foundRequirement {
		t.Fatal("missing managed Java debugger was not advertised for installation")
	}
	if options := missing.ServerInitializations()["jdtls"]; options != nil {
		t.Fatalf("missing Java debug bundle initialized JDT LS: %#v", options)
	}

	directory := filepath.Join(root, "java-debug")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	oldBundle := filepath.Join(directory, "com.microsoft.java.debug.plugin-0.52.0.jar")
	bundle := filepath.Join(directory, "com.microsoft.java.debug.plugin-0.53.1.jar")
	for _, path := range []string{oldBundle, bundle} {
		if err := os.WriteFile(path, []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	installed := NewRegistry(toolDirectoryStub{"java-debug": root})
	want := map[string]any{"bundles": []string{bundle}}
	if got := installed.ServerInitializations()["jdtls"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("managed JDT LS initialization = %#v, want %#v", got, want)
	}
}

func TestManagedRegistryIgnoresExternalAdapterOverrides(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "external-java-debug.jar")
	if err := os.WriteFile(bundle, []byte("jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINGMAN_JAVA_DEBUG_BUNDLE", bundle)
	t.Setenv("WINGMAN_JS_DEBUG_SERVER", bundle)
	t.Setenv("WINGMAN_JS_DEBUG_ADAPTER", "/external/js-debug-adapter")
	registry := NewRegistry(toolDirectoryStub{})
	if options := registry.ServerInitializations()["jdtls"]; options != nil {
		t.Fatalf("external Java bundle was loaded: %+v", options)
	}
	commands := make(map[string]string)
	for _, adapter := range registry.Descriptors() {
		commands[adapter.Name] = adapter.Command
	}
	if commands["java-debug"] != "java-debug-adapter" || commands["vscode-js-debug"] != "js-debug-adapter" {
		t.Fatalf("managed adapter commands = %v", commands)
	}
}

func TestRegistryIgnoresExplicitJavaScriptDebugServerWithoutToolDirectory(t *testing.T) {
	server := filepath.Join(t.TempDir(), "dapDebugServer.js")
	if err := os.WriteFile(server, []byte("// server"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINGMAN_JS_DEBUG_SERVER", server)
	registry := NewRegistry()
	found := false
	for _, descriptor := range registry.Descriptors() {
		if descriptor.Name == "vscode-js-debug" {
			found = descriptor.Command == "js-debug-adapter" &&
				descriptor.Transport == "tcp" &&
				descriptor.ReadyPrefix == "Debug server listening at " &&
				reflect.DeepEqual(descriptor.Args, []string{"0", "127.0.0.1"})
		}
	}
	if !found {
		t.Fatal("managed JavaScript debug server was not configured")
	}
}

func TestRegistryUsesJavaScriptAdapterCommandByDefault(t *testing.T) {
	t.Setenv("WINGMAN_JS_DEBUG_ADAPTER", "")
	t.Setenv("WINGMAN_JS_DEBUG_SERVER", "")
	for _, descriptor := range NewRegistry().Descriptors() {
		if descriptor.Name != "vscode-js-debug" {
			continue
		}
		if descriptor.Command != "js-debug-adapter" || !reflect.DeepEqual(descriptor.Args, []string{"0", "127.0.0.1"}) {
			t.Fatalf("JavaScript adapter = %#v", descriptor)
		}
		return
	}
	t.Fatal("JavaScript adapter was not registered")
}

type stringPort string

func (value stringPort) String() string { return string(value) }

func TestJavaDebugPortValidation(t *testing.T) {
	for _, value := range []any{4711, int16(4711), uint16(4711), uint64(4711), float64(4711), "4711", stringPort("4711"), map[string]any{"port": float64(4711)}} {
		port, err := javaDebugPort(value)
		if err != nil || port != 4711 {
			t.Fatalf("javaDebugPort(%#v) = %d, %v", value, port, err)
		}
	}
	for _, value := range []any{nil, 0, 65536, 12.5, "remote.example:4711"} {
		if _, err := javaDebugPort(value); err == nil {
			t.Fatalf("javaDebugPort(%#v) succeeded", value)
		}
	}
}

type javaCommandExecutor struct {
	results  map[string]any
	commands []lsp.Command
}

func (executor *javaCommandExecutor) ExecuteCommand(_ context.Context, _ string, _ *string, command lsp.Command) (any, error) {
	executor.commands = append(executor.commands, command)
	return executor.results[command.Command], nil
}

func TestJavaConnectorPreparesLaunchWithJDTLS(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src", "main", "java", "example", "App.java")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package example; public class App { public static void main(String[] args) {} }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	classes := filepath.Join(root, "target", "classes")
	executor := &javaCommandExecutor{results: map[string]any{
		"vscode.java.resolveClasspath":      json.RawMessage(fmt.Sprintf(`[%s,%s]`, `[]`, mustJSON(t, []string{classes}))),
		"vscode.java.resolveJavaExecutable": json.RawMessage(mustJSON(t, "/jdk/bin/java")),
	}}
	original := map[string]any{"projectName": "example-project"}
	prepared, err := NewConnector(executor).PrepareAdapter(context.Background(), dap.Plan{
		Adapter: dap.AdapterDescriptor{Name: "java-debug"}, ProjectDir: root,
		Target: "example.App", Request: "launch", Arguments: original,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, mutated := original["classPaths"]; mutated {
		t.Fatalf("PrepareAdapter mutated the caller's arguments: %#v", original)
	}
	if got := prepared.Arguments["modulePaths"]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("modulePaths = %#v", got)
	}
	if got := prepared.Arguments["classPaths"]; !reflect.DeepEqual(got, []string{classes}) {
		t.Fatalf("classPaths = %#v", got)
	}
	if got := prepared.Arguments["javaExec"]; got != "/jdk/bin/java" {
		t.Fatalf("javaExec = %#v", got)
	}
	wantCommands := []string{"vscode.java.resolveClasspath", "vscode.java.resolveJavaExecutable"}
	if len(executor.commands) != len(wantCommands) {
		t.Fatalf("commands = %#v", executor.commands)
	}
	for index, want := range wantCommands {
		if executor.commands[index].Command != want {
			t.Fatalf("command %d = %q, want %q", index, executor.commands[index].Command, want)
		}
		var mainClass, projectName string
		if err := json.Unmarshal(executor.commands[index].Arguments[0], &mainClass); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(executor.commands[index].Arguments[1], &projectName); err != nil {
			t.Fatal(err)
		}
		if mainClass != "example.App" || projectName != "example-project" {
			t.Fatalf("command arguments = %q, %q", mainClass, projectName)
		}
	}
}

func TestJavaConnectorPreservesExplicitLaunchPaths(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "App.java")
	if err := os.WriteFile(source, []byte("class App {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := &javaCommandExecutor{results: map[string]any{
		"vscode.java.resolveJavaExecutable": json.RawMessage(`"/jdk/bin/java"`),
	}}
	prepared, err := NewConnector(executor).PrepareAdapter(context.Background(), dap.Plan{
		Adapter: dap.AdapterDescriptor{Name: "java-debug"}, ProjectDir: root,
		Target: "App", Request: "launch", Arguments: map[string]any{"classPaths": []string{"classes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := prepared.Arguments["classPaths"]; !reflect.DeepEqual(got, []string{"classes"}) {
		t.Fatalf("classPaths = %#v", got)
	}
	if len(executor.commands) != 1 || executor.commands[0].Command != "vscode.java.resolveJavaExecutable" {
		t.Fatalf("commands = %#v", executor.commands)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
