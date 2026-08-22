package tooling

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectProjectsScansAllSpecsOnceAndSkipsGeneratedTrees(t *testing.T) {
	root := t.TempDir()
	write := func(relative string) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("services/go/go.mod")
	write("services/go/main.go")
	write("services/web/package.json")
	write("services/web/src/main.ts")
	write("node_modules/ignored/package.json")

	projects, err := DetectProjects(context.Background(), root, []ProjectSpec{
		{Markers: []string{"go.mod"}, Extensions: []string{".go"}},
		{Markers: []string{"package.json"}, Extensions: []string{".ts"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{filepath.Join(root, "services", "go")},
		{filepath.Join(root, "services", "web")},
	}
	if !reflect.DeepEqual(projects, want) {
		t.Fatalf("projects = %#v, want %#v", projects, want)
	}
}

func TestDetectProjectsAddsWorkspaceForSourcesOutsideNestedProject(t *testing.T) {
	root := t.TempDir()
	for relative := range map[string]bool{"service/pyproject.toml": true, "service/main.py": true, "tool.py": true} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	projects, err := DetectProject(context.Background(), root, ProjectSpec{Markers: []string{"pyproject.toml"}, Extensions: []string{".py"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root, filepath.Join(root, "service")}
	if !reflect.DeepEqual(projects, want) {
		t.Fatalf("projects = %#v, want %#v", projects, want)
	}
}
