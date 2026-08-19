package debugadapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestPythonAdapterFindsEditorBundledDebugpy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	adapterPath := filepath.Join(home, ".vscode", "extensions", "ms-python.debugpy-2026.6.0", "bundled", "libs", "debugpy", "adapter")
	if err := os.MkdirAll(adapterPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapterPath, "__main__.py"), []byte("# adapter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor := newPythonAdapter().Descriptor()
	wantCommand := "python3"
	if runtime.GOOS == "windows" {
		wantCommand = "python"
	}
	if descriptor.FallbackCommand != wantCommand || len(descriptor.FallbackArgs) != 1 || descriptor.FallbackArgs[0] != adapterPath {
		t.Fatalf("Python descriptor = %#v", descriptor)
	}
}

func TestGoAdapterFindsMainAndTestTargets(t *testing.T) {
	registry := NewRegistry()
	mainTargets, err := registry.DetectFile("cmd/demo/main.go", []byte(`package main

func helper() {}
func main() { helper() }
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(mainTargets) != 1 || mainTargets[0].Kind != "main" || mainTargets[0].Directory != "cmd/demo" || mainTargets[0].Line != 4 {
		t.Fatalf("main targets = %#v", mainTargets)
	}

	testTargets, err := registry.DetectFile("thing_test.go", []byte(`package thing
import "testing"
func TestThing(t *testing.T) {}
func Testhelper(t *testing.T) {}
func BenchmarkThing(b *testing.B) {}
func FuzzThing(f *testing.F) {}
func ExampleThing() {
    // Output:
}
func TestWrongSignature() {}
`))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(testTargets))
	for _, target := range testTargets {
		names = append(names, target.Name)
	}
	if want := []string{"TestThing", "BenchmarkThing", "FuzzThing", "ExampleThing"}; !slices.Equal(names, want) {
		t.Fatalf("test target names = %v, want %v", names, want)
	}
}

func TestPythonAdapterFindsExplicitAndConventionalScripts(t *testing.T) {
	registry := NewRegistry()
	targets, err := registry.DetectFile("tools/report.py", []byte("import json\n\nif __name__ == '__main__':\n    print(json.dumps({}))\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Language != "Python" || targets[0].Line != 3 {
		t.Fatalf("targets = %#v", targets)
	}

	targets, err = registry.DetectFile("app.py", []byte("# entrypoint\n\nfrom service import run\nrun()\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Line != 3 {
		t.Fatalf("conventional targets = %#v", targets)
	}

	targets, err = registry.DetectFile("library.py", []byte("def helper():\n    pass\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("library targets = %#v", targets)
	}
}

func TestJavaAdapterFindsQualifiedMainClass(t *testing.T) {
	targets, err := NewRegistry().DetectFile("src/main/java/demo/App.java", []byte(`package demo;

public final class App {
    String example = "public static void main(String[] args)";
    public static void main(String... args) {
        System.out.println("ready");
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Language != "Java" || targets[0].Name != "demo.App" || targets[0].Line != 5 {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestJavaAdapterRejectsNonPublicMainAndNamesNestedClass(t *testing.T) {
	targets, err := NewRegistry().DetectFile("src/demo/App.java", []byte(`package demo;
public class App {
    static void main(String[] ignored) {}
    public static class Nested {
        public static void main(String args[]) {}
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "demo.App$Nested" || targets[0].Line != 5 {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestJavaAdapterDoesNotTreatSiblingClassAsEnclosing(t *testing.T) {
	targets, err := NewRegistry().DetectFile("src/demo/App.java", []byte(`package demo;
class Helper {}
public class App {
    public static void main(String[] args) {}
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "demo.App" {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestRustAdapterFindsCargoEntrypoints(t *testing.T) {
	targets, err := NewRegistry().DetectFile("src/bin/report.rs", []byte(`const EXAMPLE: &str = "fn main() {}";

#[tokio::main]
async fn main() {
    println!("ready");
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Language != "Rust" || targets[0].Name != "report" || targets[0].Kind != "main" || targets[0].Line != 4 {
		t.Fatalf("targets = %#v", targets)
	}

	targets, err = NewRegistry().DetectFile("examples/debug/rust/src/main.rs", []byte("fn main() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "main" || targets[0].Kind != "main" {
		t.Fatalf("workspace folder names changed Rust source detection: %#v", targets)
	}
}

func TestDotnetAdapterFindsMainAndTopLevelPrograms(t *testing.T) {
	registry := NewRegistry()
	targets, err := registry.DetectFile("Console/Program.cs", []byte(`namespace ConsoleApp;
public static class Program {
    public static async Task<int> Main(string[] args) {
        await Task.Yield();
        return 0;
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Language != dotnetLanguage || targets[0].Line != 3 {
		t.Fatalf("main targets = %#v", targets)
	}

	targets, err = registry.DetectFile("TopLevel/Program.cs", []byte(`using System;

var message = "ready";
Console.WriteLine(message);
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Detail != ".NET top-level program" || targets[0].Line != 3 {
		t.Fatalf("top-level targets = %#v", targets)
	}

	targets, err = registry.DetectFile("Library/Program.cs", []byte(`public class Program
{
    public string Message => "not an entry point";
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("type-only Program.cs targets = %#v", targets)
	}
}

func TestJavaScriptAdapterFindsNodeAndViteTargets(t *testing.T) {
	registry := NewRegistry()
	targets, err := registry.DetectFile("tools/main.ts", []byte(`const message: string = "ready";
console.log(message);
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Language != javascriptLanguage || targets[0].Kind != "node" {
		t.Fatalf("TypeScript targets = %#v", targets)
	}

	targets, err = registry.DetectFile("web/vite.config.ts", []byte(`import { defineConfig } from "vite";
export default defineConfig({ server: { port: 4173 } });
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Kind != "vite" || targets[0].Line != 1 {
		t.Fatalf("Vite targets = %#v", targets)
	}

	targets, err = registry.DetectFile("web/src/main.tsx", []byte(`import { createRoot } from "react-dom/client";
createRoot(document.body).render(<main />);
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("React source was mistaken for a Node entry point: %#v", targets)
	}
}

func TestRegistryDetectWorkspaceSkipsGeneratedTrees(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cmd/a/main.go", "package main\nfunc main() {}\n")
	write("app.py", "print('ready')\n")
	write("vendor/ignored/main.go", "package main\nfunc main() {}\n")

	targets, err := NewRegistry().DetectWorkspace(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Path != "app.py" || targets[1].Path != "cmd/a/main.go" {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestCheckedInDebuggerSamplesExposeRunnableTargets(t *testing.T) {
	tests := []struct {
		path     string
		language string
		kind     string
	}{
		{path: "examples/debug/go/main.go", language: "Go", kind: "main"},
		{path: "examples/debug/python/main.py", language: "Python", kind: "script"},
		{path: "examples/debug/java/src/main/java/example/App.java", language: "Java", kind: "main"},
		{path: "examples/debug/rust/src/main.rs", language: "Rust", kind: "main"},
		{path: "examples/debug/dotnet/Program.cs", language: dotnetLanguage, kind: "main"},
		{path: "examples/debug/typescript/src/main.ts", language: javascriptLanguage, kind: "node"},
		{path: "examples/debug/react-vite/vite.config.ts", language: javascriptLanguage, kind: "vite"},
	}

	registry := NewRegistry()
	for _, test := range tests {
		t.Run(test.language+"/"+test.kind, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(test.path)))
			if err != nil {
				t.Fatal(err)
			}
			targets, err := registry.DetectFile(test.path, contents)
			if err != nil {
				t.Fatal(err)
			}
			if len(targets) != 1 || targets[0].Language != test.language || targets[0].Kind != test.kind {
				t.Fatalf("targets = %#v, want one %s %s target", targets, test.language, test.kind)
			}
		})
	}
}
