package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindProjectUsesDeepestMatchingRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "web")
	projects := []projectRoot{
		{Dir: root, Server: Server{Command: "gopls", Args: []string{"serve"}, Languages: []string{"go"}}},
		{Dir: nested, Server: Server{Command: "gopls", Args: []string{"serve"}, Languages: []string{"go"}}},
	}

	project := findProject(projects, filepath.Join(nested, "MAIN.GO"))
	if project == nil || project.Dir != nested {
		t.Fatalf("findProject = %+v, want nested project %q", project, nested)
	}
}

func TestProjectKeyIncludesServerArguments(t *testing.T) {
	dir := t.TempDir()
	left := projectRoot{Dir: dir, Server: Server{Command: "server", Args: []string{"--stdio"}}}
	right := projectRoot{Dir: dir, Server: Server{Command: "server", Args: []string{"lsp", "--stdio"}}}
	if projectKey(left) == projectKey(right) {
		t.Fatal("project keys should differ when server arguments differ")
	}
}

func TestDiscoverSourceFilesAssignsNestedProjectOnce(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "web")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "main.go"), filepath.Join(nested, "app.go")} {
		if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	server := Server{Command: "gopls", Args: []string{"serve"}, Languages: []string{"go"}}
	projects := []projectRoot{{Dir: root, Server: server}, {Dir: nested, Server: server}}
	for _, project := range projects {
		projectID := projectKey(project)
		files, total, truncated := discoverSourceFilesMatching(context.Background(), project.Dir, project.Server.Languages, 50, func(path string) bool {
			owner := findProject(projects, path)
			return owner != nil && projectKey(*owner) == projectID
		})
		if truncated || total != 1 || len(files) != 1 {
			t.Fatalf("project %q: files=%v total=%d truncated=%v, want one file", project.Dir, files, total, truncated)
		}
		if filepath.Dir(files[0]) != project.Dir {
			t.Fatalf("project %q received file %q owned by another root", project.Dir, files[0])
		}
	}
}

func TestFormatLocationsIncludesSourceText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc run() {}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	locations := []Location{
		{URI: FileURI(path), Range: Range{Start: Position{Line: 2, Character: 5}}},
	}

	result := formatLocations("References", locations, dir)

	if !strings.Contains(result, "References (1 found across 1 files):") {
		t.Errorf("missing header: %q", result)
	}
	if !strings.Contains(result, "main.go:3:6: func run() {}") {
		t.Errorf("missing flat location with source text: %q", result)
	}
}

func TestFormatLocationsCapsOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	var locations []Location
	for range maxListedLocations + 20 {
		locations = append(locations, Location{URI: FileURI(path), Range: Range{Start: Position{Line: 0}}})
	}

	result := formatLocations("References", locations, dir)

	note := fmt.Sprintf("showing first %d of %d locations", maxListedLocations, maxListedLocations+20)
	if !strings.Contains(result, note) {
		t.Errorf("missing truncation note %q in: %q", note, result)
	}
	if got := strings.Count(result, "main.go:1:1"); got != maxListedLocations {
		t.Errorf("listed %d locations, want %d", got, maxListedLocations)
	}
}

func TestFormatDefinitionsIncludesSnippet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc run() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	locations := []Location{
		{URI: FileURI(path), Range: Range{Start: Position{Line: 2, Character: 5}}},
	}

	result := formatDefinitions(locations, dir)

	if !strings.Contains(result, "main.go:3:6") {
		t.Errorf("missing location: %q", result)
	}
	if !strings.Contains(result, "func run() {") {
		t.Errorf("missing snippet: %q", result)
	}
	if !strings.Contains(result, "println(\"hi\")") {
		t.Errorf("snippet should span multiple lines: %q", result)
	}
}

func TestMatchSymbol(t *testing.T) {
	candidates := []symbolCandidate{
		{name: "Manager", qualified: "Manager", position: Position{Line: 10}},
		{name: "Close", qualified: "Manager.Close", position: Position{Line: 20}},
		{name: "(*Session).Close", qualified: "(*Session).Close", position: Position{Line: 30}},
	}

	if c, ok := matchSymbol(candidates, "Manager"); !ok || c.position.Line != 10 {
		t.Errorf("Manager: got %+v %v", c, ok)
	}
	if c, ok := matchSymbol(candidates, "Close"); !ok || c.position.Line != 20 {
		t.Errorf("Close: got %+v %v", c, ok)
	}
	if c, ok := matchSymbol(candidates, "Manager.Close"); !ok || c.position.Line != 20 {
		t.Errorf("Manager.Close: got %+v %v", c, ok)
	}
	if c, ok := matchSymbol(candidates, "Session.Close"); !ok || c.position.Line != 30 {
		t.Errorf("Session.Close: got %+v %v", c, ok)
	}
	if _, ok := matchSymbol(candidates, "Missing"); ok {
		t.Error("Missing should not match")
	}
}

func TestDiscoverSourceFilesReportsTotal(t *testing.T) {
	dir := t.TempDir()
	for i := range 10 {
		path := filepath.Join(dir, fmt.Sprintf("file%d.go", i))
		if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	files, total, truncated := discoverSourceFilesMatching(context.Background(), dir, []string{"go"}, 4, nil)
	if len(files) != 4 {
		t.Errorf("len(files) = %d, want 4", len(files))
	}
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
}

func TestDiagnosticProviderEnabled(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "null", want: false},
		{value: "false", want: false},
		{value: "true", want: true},
		{value: `{}`, want: true},
	} {
		if got := diagnosticProviderEnabled([]byte(test.value)); got != test.want {
			t.Errorf("diagnosticProviderEnabled(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}
