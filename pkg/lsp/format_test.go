package lsp_test

import (
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestFormatDiagnostics(t *testing.T) {
	diags := []Diagnostic{
		{
			Range:    Range{Start: Position{Line: 12, Character: 0}},
			Severity: DiagnosticSeverityHint,
			Message:  "could simplify",
		},
		{
			Range:    Range{Start: Position{Line: 4, Character: 10}},
			Severity: DiagnosticSeverityError,
			Source:   "compiler",
			Message:  "undefined: bar",
		},
		{
			Range:    Range{Start: Position{Line: 12, Character: 0}},
			Severity: DiagnosticSeverityWarning,
			Message:  "unused variable",
		},
	}

	result := FormatDiagnostics(diags, "/home/user/project/main.go", "/home/user/project")

	if !strings.Contains(result, "Diagnostics (3 found: 1 error, 1 warning, 1 hint):") {
		t.Errorf("expected header with severity counts, got: %q", result)
	}
	if !strings.Contains(result, "main.go:5:11") {
		t.Error("expected 1-based line:col for error diagnostic")
	}
	if !strings.Contains(result, "[compiler]") {
		t.Error("expected source tag")
	}
	if !strings.Contains(result, "main.go:13:1") {
		t.Error("expected 1-based line:col for warning diagnostic")
	}

	errIdx := strings.Index(result, "Error")
	warnIdx := strings.Index(result, "Warning")
	hintIdx := strings.Index(result, "Hint")
	if errIdx == -1 || warnIdx == -1 || hintIdx == -1 || !(errIdx < warnIdx && warnIdx < hintIdx) {
		t.Errorf("expected severity ordering error < warning < hint, got: %q", result)
	}
}

func TestDiagnosticSeverityName(t *testing.T) {
	tests := []struct {
		severity int
		want     string
	}{
		{DiagnosticSeverityError, "Error"},
		{DiagnosticSeverityWarning, "Warning"},
		{DiagnosticSeverityInformation, "Info"},
		{DiagnosticSeverityHint, "Hint"},
		{0, "Unknown"},
		{99, "Unknown"},
	}

	for _, tt := range tests {
		got := DiagnosticSeverityName(tt.severity)
		if got != tt.want {
			t.Errorf("DiagnosticSeverityName(%d) = %q, want %q", tt.severity, got, tt.want)
		}
	}
}
