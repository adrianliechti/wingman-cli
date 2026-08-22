package debugadapter

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGoAdapterFindsMainAndTestTargets(t *testing.T) {
	registry := NewRegistry(nil)
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
	registry := NewRegistry(nil)
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
	targets, err := NewRegistry(nil).DetectFile("src/main/java/demo/App.java", []byte(`package demo;

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
	targets, err := NewRegistry(nil).DetectFile("src/demo/App.java", []byte(`package demo;
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
	targets, err := NewRegistry(nil).DetectFile("src/demo/App.java", []byte(`package demo;
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
	targets, err := NewRegistry(nil).DetectFile("src/bin/report.rs", []byte(`const EXAMPLE: &str = "fn main() {}";

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

	targets, err = NewRegistry(nil).DetectFile("examples/debug/rust/src/main.rs", []byte("fn main() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "main" || targets[0].Kind != "main" {
		t.Fatalf("workspace folder names changed Rust source detection: %#v", targets)
	}
}

func TestDotnetAdapterFindsMainAndTopLevelPrograms(t *testing.T) {
	registry := NewRegistry(nil)
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

func TestJavaScriptAdapterFindsNodeAndPackageScriptTargets(t *testing.T) {
	registry := NewRegistry(nil)
	targets, err := registry.DetectFile("tools/main.ts", []byte(`const message: string = "ready";
console.log(message);
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Language != javascriptLanguage || targets[0].Kind != "node" {
		t.Fatalf("TypeScript targets = %#v", targets)
	}

	targets, err = registry.DetectFile("web/package.json", []byte(`{
  "scripts": {
    "build": "vite build",
    "dev": "vite --port 4173",
    "preview": "vite preview",
    "server": "tsx src/server.ts"
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Kind != "browser-script" || targets[0].Name != "dev" || targets[0].Line != 4 || targets[1].Kind != "node-script" || targets[1].Name != "server" || targets[1].Line != 6 {
		t.Fatalf("package script targets = %#v", targets)
	}

	targets, err = registry.DetectFile("web/vite.config.ts", []byte(`export default { server: { port: 4173 } }`))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("Vite config remained a launch target: %#v", targets)
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

	targets, err := NewRegistry(nil).DetectWorkspace(context.Background(), root)
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
		{path: "examples/debug/typescript/package.json", language: javascriptLanguage, kind: "node-script"},
		{path: "examples/debug/react-vite/package.json", language: javascriptLanguage, kind: "browser-script"},
	}

	registry := NewRegistry(nil)
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

func writeDiscoveryTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
