package language

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestDiagnosticsFromProtocol(t *testing.T) {
	raw := []lsp.Diagnostic{{
		Range:    lsp.Range{Start: lsp.Position{Line: 3, Character: 5}, End: lsp.Position{Line: 3, Character: 9}},
		Severity: lsp.DiagnosticSeverityWarning,
		Source:   lsp.Optional("compiler"),
		Message:  lsp.String("unused value"),
	}}
	values := DiagnosticsFromProtocol(raw)
	if len(values) != 1 || values[0].Range.Start.Line != 3 || values[0].Range.End.Character != 9 || values[0].Severity != SeverityWarning || values[0].Source != "compiler" || values[0].Message != "unused value" {
		t.Fatalf("diagnostics = %+v", values)
	}
}

func TestFormatDiagnostics(t *testing.T) {
	values := []Diagnostic{
		{Range: Range{Start: Position{Line: 12}}, Severity: SeverityHint, Message: "could simplify"},
		{Range: Range{Start: Position{Line: 4, Character: 10}}, Severity: SeverityError, Source: "compiler", Message: "undefined: bar"},
		{Range: Range{Start: Position{Line: 12}}, Severity: SeverityWarning, Message: "unused variable"},
	}
	result := FormatDiagnostics(values, "/home/user/project/main.go", "/home/user/project")
	for _, want := range []string{"Diagnostics (3 found: 1 error, 1 warning, 1 hint):", "main.go:5:11", "[compiler]", "main.go:13:1"} {
		if !strings.Contains(result, want) {
			t.Fatalf("format %q does not contain %q", result, want)
		}
	}
	if errorIndex, warningIndex, hintIndex := strings.Index(result, "Error"), strings.Index(result, "Warning"), strings.Index(result, "Hint"); errorIndex < 0 || warningIndex < errorIndex || hintIndex < warningIndex {
		t.Fatalf("diagnostics are not severity sorted: %q", result)
	}
}

func TestDiscoverDiagnosticFilesAssignsNestedProjectOnce(t *testing.T) {
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
	server := lsp.Server{Command: "gopls", Args: []string{"serve"}, Languages: []string{"go"}}
	projects := []lsp.Project{{Dir: root, Server: server}, {Dir: nested, Server: server}}
	for _, project := range projects {
		projectID := diagnosticProjectKey(project)
		files, total, truncated := discoverDiagnosticFiles(context.Background(), project.Dir, project.Server.Languages, 50, func(path string) bool {
			owner := diagnosticProject(projects, path)
			return owner != nil && diagnosticProjectKey(*owner) == projectID
		})
		if truncated || total != 1 || len(files) != 1 || filepath.Dir(files[0]) != project.Dir {
			t.Fatalf("project %q: files=%v total=%d truncated=%v", project.Dir, files, total, truncated)
		}
	}
}

func TestDiscoverDiagnosticFilesReportsTotal(t *testing.T) {
	dir := t.TempDir()
	for i := range 10 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.go", i)), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, total, truncated := discoverDiagnosticFiles(context.Background(), dir, []string{"go"}, 4, nil)
	if len(files) != 4 || total != 10 || truncated {
		t.Fatalf("files=%d total=%d truncated=%v", len(files), total, truncated)
	}
}
