package graph

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

var importSampleFiles = map[string]string{
	"go":         "x.go",
	"python":     "x.py",
	"javascript": "x.js",
	"typescript": "x.ts",
	"tsx":        "x.tsx",
}

func TestImportQueriesCompile(t *testing.T) {
	for lang, query := range importQueries {
		entry := grammars.DetectLanguage(importSampleFiles[lang])
		if entry == nil {
			t.Fatalf("no grammar detected for %s", lang)
		}
		if _, err := gotreesitter.NewQuery(query, entry.Language()); err != nil {
			t.Errorf("import query for %s failed to compile: %v", lang, err)
		}
	}
}

func TestGoImportsAndDeps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", `package main

import (
	"fmt"
	"myrepo/util"
)

func main() { fmt.Println(util.Help()) }
`)
	writeFile(t, root, "util/helper.go", `package util

func Help() string { return "ok" }
`)
	ctx := context.Background()
	e := New(root, filepath.Join(t.TempDir(), "g.json"))
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	root_, err := e.Deps(ctx, "main.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(root_.DependsOn, "util") {
		t.Fatalf("expected . to depend on util, got %+v", root_)
	}
	if !slices.Contains(root_.External, "fmt") {
		t.Fatalf("expected fmt as external import, got %+v", root_.External)
	}

	util, err := e.Deps(ctx, "util", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(util.DependedBy, ".") {
		t.Fatalf("expected util depended-by ., got %+v", util)
	}
}

func TestPythonImportsResolve(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/sub/a.py", `from ..other import thing

def run():
    return thing()
`)
	writeFile(t, root, "pkg/other/b.py", `def thing():
    return 1
`)
	ctx := context.Background()
	e := New(root, filepath.Join(t.TempDir(), "g.json"))
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	d, err := e.Deps(ctx, "pkg/sub", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(d.DependsOn, "pkg/other") {
		t.Fatalf("expected pkg/sub -> pkg/other (relative import), got %+v", d)
	}
}

func TestTransitiveDeps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/a.go", "package a\nimport \"m/b\"\nfunc A() {}\n")
	writeFile(t, root, "b/b.go", "package b\nimport \"m/c\"\nfunc B() {}\n")
	writeFile(t, root, "c/c.go", "package c\nfunc C() {}\n")
	ctx := context.Background()
	e := New(root, filepath.Join(t.TempDir(), "g.json"))
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	d, err := e.Deps(ctx, "a", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(d.DependsOn, "b") {
		t.Fatalf("expected a -> b direct, got %+v", d.DependsOn)
	}
	if !slices.Contains(d.Transitive, "c") {
		t.Fatalf("expected c in transitive deps of a, got %+v", d.Transitive)
	}
}
