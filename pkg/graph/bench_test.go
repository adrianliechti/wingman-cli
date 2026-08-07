package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBenchIndex indexes a real repo end-to-end for profiling. Run with:
//
//	BENCH_REPO=/path/to/repo go test ./pkg/graph -run TestBenchIndex -v -timeout 30m
func TestBenchIndex(t *testing.T) {
	root := os.Getenv("BENCH_REPO")
	if root == "" {
		t.Skip("set BENCH_REPO to a repo path")
	}

	ctx := context.Background()

	start := time.Now()
	g, files, stats, err := indexRepo(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	indexed := time.Since(start)

	cache := filepath.Join(t.TempDir(), "graph.json")
	if err := saveSnapshot(cache, g, files, stats.Files, stats.Skipped, time.Now()); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(cache)

	via := map[Provenance]int{}
	for _, e := range g.Edges {
		v := e.Via
		if v == "" {
			v = ViaName
		}
		via[v]++
	}

	t.Logf("repo=%s", root)
	t.Logf("files=%d nodes=%d edges=%d imports=%d", stats.Files, stats.Nodes, stats.Edges, len(g.Imports))
	t.Logf("index time: %s (%.1f files/s)", indexed.Round(time.Millisecond), float64(len(files))/indexed.Seconds())
	t.Logf("snapshot size: %.1f MB", float64(fi.Size())/(1<<20))
	t.Logf("edges by provenance: %v", via)
}
