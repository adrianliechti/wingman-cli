package graph

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

func TestBuiltinNamesDerivedFromGrammars(t *testing.T) {
	cases := map[string][]string{
		"x.go": {"append", "len", "panic"},
		"x.py": {"len", "print", "isinstance"},
		"x.js": {"require"},
	}
	for file, want := range cases {
		entry := grammars.DetectLanguage(file)
		if entry == nil {
			t.Fatalf("no grammar for %s", file)
		}
		names := builtinNames(entry.Name, entry.HighlightQuery)
		for _, name := range want {
			if !names[name] {
				t.Errorf("%s builtins missing %q (got %d names)", entry.Name, name, len(names))
			}
		}
	}
}

func TestGoBuiltinBareCallsDoNotBind(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/tracker.go", `package a

type tracker struct{ items []string }

func (t *tracker) append(s string) {
	t.items = append(t.items, s)
}

func use(tr *tracker) {
	tr.append("x")
}
`)
	writeFile(t, root, "b/other.go", `package b

func fill() []int {
	out := []int{}
	out = append(out, 1)
	return out
}
`)

	e := New(root, filepath.Join(t.TempDir(), "graph.json"))
	ctx := context.Background()

	res, err := e.Neighborhood(ctx, "", "append", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 1 || res.Callers[0].Name != "use" {
		t.Fatalf("append callers = %+v, want only the selector call from use", res.Callers)
	}
}

func TestPythonBuiltinBareCallsDoNotBind(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "d/shadow.py", `def len(value):
    return 0

def helper():
    return 1
`)
	writeFile(t, root, "d/use.py", `def run(data):
    return len(data) + helper()
`)

	e := New(root, filepath.Join(t.TempDir(), "graph.json"))
	ctx := context.Background()

	res, err := e.Neighborhood(ctx, "", "len", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 0 {
		t.Fatalf("shadowed len callers = %+v, want none", res.Callers)
	}

	res, err = e.Neighborhood(ctx, "", "helper", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 1 || res.Callers[0].Name != "run" {
		t.Fatalf("helper callers = %+v, want run", res.Callers)
	}
}

func TestGoUnexportedNamesBindWithinPackageOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p1/p1.go", `package p1

func helper() int { return 1 }

func Caller() int { return helper() }
`)
	writeFile(t, root, "p2/p2.go", `package p2

func helper() int { return 2 }
`)

	e := New(root, filepath.Join(t.TempDir(), "graph.json"))
	ctx := context.Background()

	res, err := e.Neighborhood(ctx, "", "helper", "p1/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 1 || res.Callers[0].Name != "Caller" {
		t.Fatalf("p1 helper callers = %+v, want Caller", res.Callers)
	}

	res, err = e.Neighborhood(ctx, "", "helper", "p2/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 0 {
		t.Fatalf("p2 helper callers = %+v, want none", res.Callers)
	}
}

func TestCallsBindOnlyAlongImports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app/main.py", `from lib.util import helper

def run():
    return helper()
`)
	writeFile(t, root, "lib/util.py", `def helper():
    return 1
`)
	writeFile(t, root, "other/dup.py", `def helper():
    return 2
`)

	e := New(root, filepath.Join(t.TempDir(), "graph.json"))
	ctx := context.Background()

	res, err := e.Neighborhood(ctx, "", "helper", "lib/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 1 || res.Callers[0].Name != "run" {
		t.Fatalf("imported helper callers = %+v, want run", res.Callers)
	}

	res, err = e.Neighborhood(ctx, "", "helper", "other/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 0 {
		t.Fatalf("non-imported helper callers = %+v, want none", res.Callers)
	}
}

func TestGoQualifierPinsModule(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", `package main

import (
	"m/alpha"
	"m/beta"
)

func main() {
	alpha.New()
	_ = beta.Version
}
`)
	writeFile(t, root, "alpha/alpha.go", `package alpha

func New() int { return 1 }
`)
	writeFile(t, root, "beta/beta.go", `package beta

var Version = "1"

func New() int { return 2 }
`)

	e := New(root, filepath.Join(t.TempDir(), "graph.json"))
	ctx := context.Background()

	res, err := e.Neighborhood(ctx, "", "New", "alpha/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 1 || res.Callers[0].Name != "main" {
		t.Fatalf("alpha.New callers = %+v, want main", res.Callers)
	}

	res, err = e.Neighborhood(ctx, "", "New", "beta/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 0 {
		t.Fatalf("beta.New callers = %+v, want none", res.Callers)
	}
}

type fakeImplResolver struct {
	impls []ResolvedLocation
}

func (f *fakeImplResolver) ResolveCall(ctx context.Context, file string, line, column int) (string, int, bool) {
	return "", 0, false
}

func (f *fakeImplResolver) ResolveImplementations(ctx context.Context, file string, line, column int) []ResolvedLocation {
	return f.impls
}

func TestInterfaceDispatchViaImplementationResolver(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p1/use.go", `package p1

type Runner interface {
	Run() error
}

func Use(r Runner) error {
	return r.Run()
}
`)
	writeFile(t, root, "p2/impl.go", `package p2

type Impl struct{}

func (i *Impl) Run() error { return nil }
`)

	ctx := context.Background()

	plain := New(root, filepath.Join(t.TempDir(), "plain.json"))
	res, err := plain.Neighborhood(ctx, "", "Run", "p2/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 0 {
		t.Fatalf("without resolver, p2 Run callers = %+v, want none", res.Callers)
	}

	resolver := &fakeImplResolver{impls: []ResolvedLocation{{File: "p2/impl.go", Line: 5}}}
	e := New(root, filepath.Join(t.TempDir(), "graph.json"), WithResolver(resolver))
	res, err = e.Neighborhood(ctx, "", "Run", "p2/")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Callers) != 1 || res.Callers[0].Name != "Use" {
		t.Fatalf("with resolver, p2 Run callers = %+v, want Use", res.Callers)
	}

	stats := e.EdgeStats()
	if stats[ViaLSP] == 0 {
		t.Fatalf("edge stats = %+v, want lsp-provenance edges", stats)
	}
}

func TestHotspotsExcludeAmbiguousEdges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "p1/a.go", `package p1

func Shared() int { return 1 }
`)
	writeFile(t, root, "p2/b.go", `package p2

func Shared() int { return 2 }
`)
	writeFile(t, root, "main.go", `package main

func main() {
	Unique()
}

func Unique() int {
	return Shared()
}
`)

	e := New(root, filepath.Join(t.TempDir(), "graph.json"))
	ctx := context.Background()

	arch, err := e.Architecture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var unique *Hotspot
	for _, h := range arch.Hotspots {
		if h.Node.Name == "Shared" {
			t.Fatalf("ambiguously called Shared listed as hotspot: %+v", h)
		}
		if h.Node.Name == "Unique" {
			unique = &h
		}
	}
	if unique == nil {
		t.Fatal("Unique missing from hotspots")
	}
	if unique.Callers != 1 || unique.Callees != 0 {
		t.Fatalf("Unique hotspot = %d in %d out, want 1 in 0 out", unique.Callers, unique.Callees)
	}
}
