package debugtarget

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGoDetectorFindsMainAndTestTargets(t *testing.T) {
	registry := NewRegistry()
	mainTargets, err := registry.DetectFile("cmd/demo/main.go", []byte(`package main

func helper() {}
func main() { helper() }
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(mainTargets) != 1 || mainTargets[0].Kind != "main" || mainTargets[0].Line != 4 {
		t.Fatalf("main targets = %#v", mainTargets)
	}

	testTargets, err := registry.DetectFile("thing_test.go", []byte(`package thing
import "testing"
func TestThing(t *testing.T) {}
func Testhelper(t *testing.T) {}
func BenchmarkThing(b *testing.B) {}
func FuzzThing(f *testing.F) {}
func ExampleThing() {}
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

func TestRegistryDetectWorkspaceSkipsNestedGeneratedTrees(t *testing.T) {
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
	write("pkg/a/a_test.go", "package a\nfunc TestA() {}\n")
	write("vendor/ignored/main.go", "package main\nfunc main() {}\n")

	targets, err := NewRegistry().DetectWorkspace(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %#v", targets)
	}
	if targets[0].Path != "cmd/a/main.go" || targets[1].Path != "pkg/a/a_test.go" {
		t.Fatalf("target paths = %#v", targets)
	}
}
