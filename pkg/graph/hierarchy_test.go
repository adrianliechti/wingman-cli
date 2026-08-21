package graph

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestHierarchyQueriesCompile(t *testing.T) {
	for lang, query := range hierarchyQueries {
		entry := grammars.DetectLanguage(importSampleFiles[lang])
		if entry == nil {
			t.Fatalf("no grammar detected for %s", lang)
		}
		if _, err := gotreesitter.NewQuery(query, entry.Language()); err != nil {
			t.Errorf("hierarchy query for %s failed to compile: %v", lang, err)
		}
	}
}

func names(nodes []*Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}

func TestTypeScriptHierarchy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "shapes.ts", `interface Shape { area(): number }
interface Drawable { draw(): void }
class Base { x = 1 }
class Circle extends Base implements Shape, Drawable {
  area() { return 1 }
  draw() {}
}
`)
	ctx := context.Background()
	e := New(root, filepath.Join(t.TempDir(), "g.json"))
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	circle, err := e.Hierarchy(ctx, "Circle", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names(circle.Extends), "Base") {
		t.Errorf("Circle should extend Base, got extends=%v", names(circle.Extends))
	}
	if !slices.Contains(names(circle.Implements), "Shape") || !slices.Contains(names(circle.Implements), "Drawable") {
		t.Errorf("Circle should implement Shape+Drawable, got %v", names(circle.Implements))
	}

	shape, _ := e.Hierarchy(ctx, "Shape", "")
	if !slices.Contains(names(shape.Implementers), "Circle") {
		t.Errorf("Shape should be implemented by Circle, got %v", names(shape.Implementers))
	}

	base, _ := e.Hierarchy(ctx, "Base", "")
	if !slices.Contains(names(base.Subtypes), "Circle") {
		t.Errorf("Base should have subtype Circle, got %v", names(base.Subtypes))
	}
}

func TestPythonHierarchy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "animals.py", `class Animal:
    def speak(self): pass

class Dog(Animal):
    def speak(self): return "woof"
`)
	ctx := context.Background()
	e := New(root, filepath.Join(t.TempDir(), "g.json"))
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	dog, err := e.Hierarchy(ctx, "Dog", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names(dog.Extends), "Animal") {
		t.Errorf("Dog should extend Animal, got %v", names(dog.Extends))
	}
}

func TestGoHierarchy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "types.go", `package main

type Base struct{ x int }

type Derived struct {
	Base
	y int
}
`)
	ctx := context.Background()
	e := New(root, filepath.Join(t.TempDir(), "g.json"))
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	d, err := e.Hierarchy(ctx, "Derived", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names(d.Extends), "Base") {
		t.Errorf("Derived should embed Base, got extends=%v", names(d.Extends))
	}
}
