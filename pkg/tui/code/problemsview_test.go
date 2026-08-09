package code

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestDiagnosticsCoverageComplete(t *testing.T) {
	coverage, partial := diagnosticsCoverage(lsp.WorkspaceDiagnosticsReport{
		CheckedFiles:    3,
		DiscoveredFiles: 3,
	})
	if partial {
		t.Fatal("complete coverage reported as partial")
	}
	if coverage != "checked 3/3 files" {
		t.Fatalf("coverage = %q", coverage)
	}
}

func TestDiagnosticsCoverageExplainsPartialResults(t *testing.T) {
	coverage, partial := diagnosticsCoverage(lsp.WorkspaceDiagnosticsReport{
		CheckedFiles:       4,
		DiscoveredFiles:    10,
		DiscoveryTruncated: true,
		UnknownFiles:       2,
		UnavailableServers: []string{"gopls (tools)"},
	})
	if !partial {
		t.Fatal("incomplete coverage was not reported as partial")
	}
	for _, want := range []string{"checked 4/10+ files", "2 unknown", "unavailable: gopls (tools)"} {
		if !strings.Contains(coverage, want) {
			t.Fatalf("coverage %q does not contain %q", coverage, want)
		}
	}
}
