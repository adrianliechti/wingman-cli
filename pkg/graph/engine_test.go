package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "main.go", `package main

func main() {
	greet("world")
}

func greet(name string) string {
	return format(name)
}

func format(s string) string {
	return "hi " + s
}
`)
	writeFile(t, root, "lib/util.py", `class Animal:
    def speak(self):
        return noise()

def noise():
    return "woof"
`)
	writeFile(t, root, "node_modules/skip.js", `function shouldNotBeIndexed() {}`)

	cache := filepath.Join(t.TempDir(), "graph.json")
	return New(root, cache)
}

func TestIndexAndSearch(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()

	status, err := e.Index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Nodes == 0 {
		t.Fatal("expected nodes after indexing")
	}

	got, err := e.Search(ctx, SearchOpts{Query: "greet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "greet" {
		t.Fatalf("search greet = %+v", got)
	}

	all, _ := e.Search(ctx, SearchOpts{})
	for _, n := range all {
		if n.Name == "shouldNotBeIndexed" {
			t.Fatal("node_modules should have been skipped")
		}
	}

	var langs = map[string]bool{}
	for _, n := range all {
		langs[n.Lang] = true
	}
	if !langs["go"] || !langs["python"] {
		t.Fatalf("expected go and python langs, got %v", langs)
	}
}

func TestSearchPagination(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	first, err := e.SearchPage(ctx, SearchOpts{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Nodes) != 2 || first.Total <= len(first.Nodes) || !first.HasMore {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := e.SearchPage(ctx, SearchOpts{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Nodes) == 0 || second.Offset != 2 {
		t.Fatalf("unexpected second page: %+v", second)
	}
	if first.Nodes[0].ID == second.Nodes[0].ID || first.Nodes[1].ID == second.Nodes[0].ID {
		t.Fatalf("pagination repeated a result: first=%v second=%v", first.Nodes, second.Nodes)
	}
}

func TestSearchRefreshesStaleCache(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	writeFile(t, e.root, "added.go", "package main\n\nfunc freshlyAdded() {}\n")
	if !e.IsStale(ctx) {
		t.Fatal("expected new source file to make graph stale")
	}
	got, err := e.Search(ctx, SearchOpts{Query: "freshlyAdded"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "freshlyAdded" {
		t.Fatalf("stale graph did not refresh: %+v", got)
	}
	if e.IsStale(ctx) {
		t.Fatal("graph remained stale after query refresh")
	}
}

func TestTraceCallPath(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	res, err := e.Trace(ctx, "main", "format", "", false, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) == 0 {
		t.Fatal("expected a path from main to format")
	}

	last := res.Paths[0].Nodes
	if last[0].Name != "main" || last[len(last)-1].Name != "format" {
		t.Fatalf("unexpected path: %+v", last)
	}

	callers, err := e.Trace(ctx, "format", "", "", true, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(callers.Paths) == 0 {
		t.Fatal("expected callers of format")
	}
}

func TestSnippetAndArchitecture(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	snip, err := e.Snippet(ctx, "greet", "")
	if err != nil {
		t.Fatal(err)
	}
	if snip.Node.Name != "greet" || snip.Code == "" {
		t.Fatalf("bad snippet: %+v", snip)
	}

	arch, err := e.Architecture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if arch.TotalFiles == 0 || len(arch.Languages) == 0 {
		t.Fatalf("bad architecture: %+v", arch)
	}
	if len(arch.EntryPoints) == 0 {
		t.Fatal("expected main as entry point")
	}
}

func TestCachePersistence(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	reopened := New(e.root, e.cachePath)
	got, err := reopened.Search(ctx, SearchOpts{Query: "greet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected cached graph to load, got %d results", len(got))
	}
}
