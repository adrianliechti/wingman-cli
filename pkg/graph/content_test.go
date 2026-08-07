package graph

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func newContentTestEngine(t *testing.T) *Engine {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "app.go", `package app

// NEEDLE outside a definition
func popular() {
	println("NEEDLE")
	println("NEEDLE")
}

func callerOne() { popular() }
func callerTwo() { popular() }
`)
	writeFile(t, root, "app_test.go", `package app

func TestNeedle() {
	println("NEEDLE")
}
`)
	writeFile(t, root, "nested.py", `def outer():
    def inner():
        return "NEEDLE"
    return inner()
`)
	writeFile(t, root, "generated.go", "package app // NEEDLE "+strings.Repeat("x", 2100))
	return New(root, filepath.Join(t.TempDir(), "graph.json"))
}

func TestSearchContentEnrichesDeduplicatesAndPreservesRawHits(t *testing.T) {
	e := newContentTestEngine(t)
	ctx := context.Background()
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	res, err := e.SearchContent(ctx, ContentSearchOpts{Pattern: "NEEDLE", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalLineHits != 6 {
		t.Fatalf("total line hits = %d, want 6: %+v", res.TotalLineHits, res)
	}
	if res.TotalResults != 3 {
		t.Fatalf("deduplicated symbols = %d, want 3: %+v", res.TotalResults, res.Hits)
	}
	if res.TotalRawResults != 2 {
		t.Fatalf("raw results = %d, want 2: %+v", res.TotalRawResults, res.Raw)
	}
	if len(res.Hits) == 0 || res.Hits[0].Node.Name != "popular" {
		t.Fatalf("popular source function should rank first: %+v", res.Hits)
	}

	var popular, inner *ContentHit
	for i := range res.Hits {
		switch res.Hits[i].Node.Name {
		case "popular":
			popular = &res.Hits[i]
		case "inner":
			inner = &res.Hits[i]
		}
	}
	if popular == nil || len(popular.MatchLines) != 2 || popular.Callers != 2 {
		t.Fatalf("popular hit was not deduplicated/enriched: %+v", popular)
	}
	if inner == nil {
		t.Fatalf("nested match did not map to tightest inner definition: %+v", res.Hits)
	}
}

func TestSearchContentPaginationFiltersAndRegex(t *testing.T) {
	e := newContentTestEngine(t)
	ctx := context.Background()

	first, err := e.SearchContent(ctx, ContentSearchOpts{Pattern: "needle", IgnoreCase: true, Glob: "*.go", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Hits) != 1 || !first.HasMore || first.TotalResults != 2 {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := e.SearchContent(ctx, ContentSearchOpts{Pattern: "NE+DLE", Regex: true, Glob: "*.go", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Hits) != 1 || second.Offset != 1 || second.Hits[0].Node.ID == first.Hits[0].Node.ID {
		t.Fatalf("unexpected second page: %+v", second)
	}
	if _, err := e.SearchContent(ctx, ContentSearchOpts{Pattern: "[", Regex: true}); err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestCoveragePersistsAndContentSearchScansSkippedFiles(t *testing.T) {
	e := newContentTestEngine(t)
	ctx := context.Background()
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}
	st := e.Status()
	if len(st.Skipped) != 1 || st.Skipped[0].File != "generated.go" || st.Skipped[0].Reason != "minified" {
		t.Fatalf("unexpected coverage: %+v", st.Skipped)
	}

	reopened := New(e.root, e.cachePath)
	cached := reopened.StatusOrLoad()
	if len(cached.Skipped) != 1 || cached.Skipped[0] != st.Skipped[0] {
		t.Fatalf("coverage did not persist: got %+v want %+v", cached.Skipped, st.Skipped)
	}
	res, err := reopened.SearchContent(ctx, ContentSearchOpts{Pattern: "NEEDLE", File: "generated.go"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalResults != 0 || res.TotalRawResults != 1 || len(res.Raw) != 1 {
		t.Fatalf("skipped file content was not preserved as raw search output: %+v", res)
	}
}
