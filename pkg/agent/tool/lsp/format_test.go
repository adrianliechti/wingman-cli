package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestFormatLocationsIncludesSourceText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri, err := lsp.ParseURI(fileuri.FromPath(path))
	if err != nil {
		t.Fatal(err)
	}
	locations := []lsp.Location{{URI: uri, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}}}}
	result := formatLocations("References", locations, dir)
	if !strings.Contains(result, "References (1 found across 1 files):") || !strings.Contains(result, "main.go:3:6: func run() {}") {
		t.Fatalf("unexpected locations: %q", result)
	}
}

func TestFormatLocationsCapsOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri, err := lsp.ParseURI(fileuri.FromPath(path))
	if err != nil {
		t.Fatal(err)
	}
	locations := make([]lsp.Location, maxListedLocations+20)
	for i := range locations {
		locations[i] = lsp.Location{URI: uri}
	}
	result := formatLocations("References", locations, dir)
	note := fmt.Sprintf("showing first %d of %d locations", maxListedLocations, len(locations))
	if !strings.Contains(result, note) || strings.Count(result, "main.go:1:1") != maxListedLocations {
		t.Fatalf("unexpected truncated locations: %q", result)
	}
}

func TestFormatDefinitionsIncludesSnippet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc run() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	uri, err := lsp.ParseURI(fileuri.FromPath(path))
	if err != nil {
		t.Fatal(err)
	}
	locations := []lsp.Location{{URI: uri, Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}}}}
	result := formatDefinitions(locations, dir)
	if !strings.Contains(result, "main.go:3:6") || !strings.Contains(result, "func run() {") || !strings.Contains(result, "println(\"hi\")") {
		t.Fatalf("unexpected definitions: %q", result)
	}
}
