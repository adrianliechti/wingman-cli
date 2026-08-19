package debugadapter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	if plan.ProjectDir != "services/api" || plan.Configuration["mode"] != "test" || plan.Configuration["program"] != "." {
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
}

func TestRustAdapterPlansCargoBinary(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "rust-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Cargo.toml"), []byte("[package]\nname = \"sample-app\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := NewRegistry().Plan("Rust", Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "rust-app",
		Target: Target{Name: "main", Kind: "main", Language: "Rust", Path: "rust-app/src/main.rs", Line: 1, Column: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	cargo, ok := plan.Configuration["cargo"].(map[string]any)
	if !ok {
		t.Fatalf("cargo configuration = %#v", plan.Configuration["cargo"])
	}
	if want := []string{"build", "--bin", "sample-app"}; !reflect.DeepEqual(cargo["args"], want) {
		t.Fatalf("cargo args = %#v, want %#v", cargo["args"], want)
	}
	if len(plan.Breakpoints) != 1 {
		t.Fatalf("breakpoints = %#v", plan.Breakpoints)
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
}

func TestViteAdapterPlansConfiguredPort(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "web")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "vite.config.ts"), []byte("export default { server: { port: 4173 } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := NewRegistry().Plan(javascriptLanguage, Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: "web",
		Target: Target{Name: "Vite browser", Kind: "vite", Language: javascriptLanguage, Path: "web/vite.config.ts", Line: 1, Column: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Configuration["type"] != "pwa-chrome" || plan.Configuration["url"] != "http://localhost:4173" || len(plan.Breakpoints) != 0 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestJavaDebugPortValidation(t *testing.T) {
	for _, value := range []any{4711, float64(4711), "4711", map[string]any{"port": float64(4711)}} {
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

func TestRegistryWiresInstalledJavaDebugBundle(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "com.microsoft.java.debug.plugin-test.jar")
	if err := os.WriteFile(bundle, []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINGMAN_JAVA_DEBUG_BUNDLE", bundle)
	registry := NewRegistry()
	if got := registry.JDTLSBundles(); !reflect.DeepEqual(got, []string{bundle}) {
		t.Fatalf("JDT LS bundles = %#v", got)
	}
	found := false
	for _, descriptor := range registry.Descriptors() {
		if descriptor.Name == "java-debug" {
			found = descriptor.Command == "jdtls" && descriptor.Transport == "connect"
		}
	}
	if !found {
		t.Fatal("installed java-debug bundle did not enable the Java descriptor")
	}
}

func TestRegistryUsesExplicitJavaScriptDebugServer(t *testing.T) {
	server := filepath.Join(t.TempDir(), "dapDebugServer.js")
	if err := os.WriteFile(server, []byte("// server"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINGMAN_JS_DEBUG_SERVER", server)
	registry := NewRegistry()
	found := false
	for _, descriptor := range registry.Descriptors() {
		if descriptor.Name == "vscode-js-debug" {
			found = descriptor.Command == "node" && reflect.DeepEqual(descriptor.Args, []string{server, "0", "127.0.0.1"})
		}
	}
	if !found {
		t.Fatal("explicit JavaScript debug server was not configured")
	}
}
