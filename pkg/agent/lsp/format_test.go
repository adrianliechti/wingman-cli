package lsp

import (
	"strings"
	"testing"
)

func TestFormatDiagnostics(t *testing.T) {
	diags := []Diagnostic{
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

	if !strings.Contains(result, "Diagnostics (2 found)") {
		t.Error("expected header with count")
	}
	if !strings.Contains(result, "main.go:5:11") {
		t.Error("expected 1-based line:col for first diagnostic")
	}
	if !strings.Contains(result, "Error") {
		t.Error("expected Error severity")
	}
	if !strings.Contains(result, "[compiler]") {
		t.Error("expected source tag")
	}
	if !strings.Contains(result, "main.go:13:1") {
		t.Error("expected 1-based line:col for second diagnostic")
	}
	if !strings.Contains(result, "Warning") {
		t.Error("expected Warning severity")
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
