package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coregraph "github.com/adrianliechti/wingman-agent/pkg/graph"
)

func TestCodeGraphSearchContentOperation(t *testing.T) {
	root := t.TempDir()
	source := `package sample

// marker outside
func Alpha() {
	println("marker")
	println("marker")
}

func Beta() { println("marker") }
`
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := coregraph.New(root, filepath.Join(t.TempDir(), "graph.json"))
	graphTool := NewTools(engine)[0]

	out, err := graphTool.Execute(context.Background(), map[string]any{
		"operation": "search_content",
		"pattern":   "marker",
		"limit":     float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Showing 1 of 2 containing symbol(s)", "Alpha (function)", "matches 5,6", "More symbols available", "Raw matches outside indexed definitions", "sample.go:3"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("output missing %q:\n%s", want, out.Content)
		}
	}
}

func TestCodeGraphSearchReportsPaginationAndRejectsNegativeOffset(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\nfunc Alpha() {}\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := coregraph.New(root, filepath.Join(t.TempDir(), "graph.json"))
	graphTool := NewTools(engine)[0]

	out, err := graphTool.Execute(context.Background(), map[string]any{
		"operation": "search",
		"limit":     float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "Showing 1 of 2 symbol(s)") || !strings.Contains(out.Content, "offset=1") {
		t.Fatalf("pagination metadata missing:\n%s", out.Content)
	}
	_, err = graphTool.Execute(context.Background(), map[string]any{
		"operation": "search",
		"offset":    float64(-1),
	})
	if err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("expected negative offset validation, got %v", err)
	}
}
