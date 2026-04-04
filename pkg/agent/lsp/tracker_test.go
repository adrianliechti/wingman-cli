package lsp

import (
	"strings"
	"testing"
)

func diag(line, col, severity int, msg string) Diagnostic {
	return Diagnostic{
		Range:    Range{Start: Position{Line: line, Character: col}},
		Severity: severity,
		Message:  msg,
	}
}

func TestDiagnosticTracker_FilterNew_NoBaseline(t *testing.T) {
	tracker := NewDiagnosticTracker()

	diags := []Diagnostic{
		diag(1, 0, DiagnosticSeverityError, "undefined variable"),
		diag(5, 0, DiagnosticSeverityWarning, "unused import"),
	}

	result := tracker.FilterNew("file:///test.go", diags)

	if len(result) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d", len(result))
	}
}

func TestDiagnosticTracker_FilterNew_WithBaseline(t *testing.T) {
	tracker := NewDiagnosticTracker()
	uri := "file:///test.go"

	// Pre-existing diagnostic
	baseline := []Diagnostic{
		diag(5, 0, DiagnosticSeverityWarning, "unused import"),
	}
	tracker.SetBaseline(uri, baseline)

	// After edit: one pre-existing + one new
	all := []Diagnostic{
		diag(1, 0, DiagnosticSeverityError, "undefined variable"),
		diag(5, 0, DiagnosticSeverityWarning, "unused import"),
	}

	result := tracker.FilterNew(uri, all)

	if len(result) != 1 {
		t.Fatalf("expected 1 new diagnostic, got %d", len(result))
	}
	if result[0].Message != "undefined variable" {
		t.Errorf("expected 'undefined variable', got %q", result[0].Message)
	}
}

func TestDiagnosticTracker_FilterNew_SortsBySeverity(t *testing.T) {
	tracker := NewDiagnosticTracker()

	diags := []Diagnostic{
		diag(3, 0, DiagnosticSeverityHint, "hint"),
		diag(1, 0, DiagnosticSeverityError, "error"),
		diag(2, 0, DiagnosticSeverityWarning, "warning"),
		diag(4, 0, DiagnosticSeverityInformation, "info"),
	}

	result := tracker.FilterNew("file:///test.go", diags)

	expected := []int{DiagnosticSeverityError, DiagnosticSeverityWarning, DiagnosticSeverityInformation, DiagnosticSeverityHint}
	for i, d := range result {
		if d.Severity != expected[i] {
			t.Errorf("position %d: expected severity %d, got %d", i, expected[i], d.Severity)
		}
	}
}

func TestDiagnosticTracker_FilterNew_CapsVolume(t *testing.T) {
	tracker := NewDiagnosticTracker()

	var diags []Diagnostic
	for i := range 20 {
		diags = append(diags, diag(i, 0, DiagnosticSeverityWarning, "warning"))
	}

	result := tracker.FilterNew("file:///test.go", diags)

	if len(result) != maxDiagnosticsPerFile {
		t.Fatalf("expected %d diagnostics (cap), got %d", maxDiagnosticsPerFile, len(result))
	}
}

func TestDiagnosticTracker_CrossTurnDeduplication(t *testing.T) {
	tracker := NewDiagnosticTracker()
	uri := "file:///test.go"

	diags := []Diagnostic{
		diag(1, 0, DiagnosticSeverityError, "error one"),
		diag(2, 0, DiagnosticSeverityError, "error two"),
	}

	// First turn: both should appear
	result := tracker.FilterNew(uri, diags)
	if len(result) != 2 {
		t.Fatalf("turn 1: expected 2, got %d", len(result))
	}
	tracker.MarkDelivered(uri, result)

	// Second turn with same diagnostics: should be empty
	result = tracker.FilterNew(uri, diags)
	if len(result) != 0 {
		t.Fatalf("turn 2: expected 0 (already delivered), got %d", len(result))
	}
}

func TestDiagnosticTracker_SetBaseline_ClearsDelivered(t *testing.T) {
	tracker := NewDiagnosticTracker()
	uri := "file:///test.go"

	diags := []Diagnostic{
		diag(1, 0, DiagnosticSeverityError, "error"),
	}

	result := tracker.FilterNew(uri, diags)
	tracker.MarkDelivered(uri, result)

	// Verify it's deduplicated
	result = tracker.FilterNew(uri, diags)
	if len(result) != 0 {
		t.Fatalf("expected 0 after delivery, got %d", len(result))
	}

	// SetBaseline clears delivered, so editing the file again allows
	// previously-delivered diagnostics through (if they're not in the baseline)
	tracker.SetBaseline(uri, nil)

	result = tracker.FilterNew(uri, diags)
	if len(result) != 1 {
		t.Fatalf("expected 1 after baseline reset, got %d", len(result))
	}
}

func TestFormatNewDiagnostics(t *testing.T) {
	diags := []Diagnostic{
		{
			Range:    Range{Start: Position{Line: 9, Character: 4}},
			Severity: DiagnosticSeverityError,
			Message:  "undefined: foo",
			Source:   "compiler",
		},
		{
			Range:    Range{Start: Position{Line: 14, Character: 0}},
			Severity: DiagnosticSeverityWarning,
			Message:  "unused variable",
			Code:     "SA4006",
			Source:   "staticcheck",
		},
	}

	result := FormatNewDiagnostics(diags, "/home/user/project/main.go", "/home/user/project")

	if !strings.Contains(result, "main.go:") {
		t.Error("expected relative path in output")
	}
	if !strings.Contains(result, "✘") {
		t.Error("expected error symbol ✘")
	}
	if !strings.Contains(result, "⚠") {
		t.Error("expected warning symbol ⚠")
	}
	if !strings.Contains(result, "[Line 10:5]") {
		t.Error("expected 1-based line:col [Line 10:5]")
	}
	if !strings.Contains(result, "undefined: foo") {
		t.Error("expected error message")
	}
	if !strings.Contains(result, "[SA4006]") {
		t.Error("expected diagnostic code")
	}
	if !strings.Contains(result, "(staticcheck)") {
		t.Error("expected source name")
	}
}

func TestFormatNewDiagnostics_Empty(t *testing.T) {
	result := FormatNewDiagnostics(nil, "/test.go", "/")
	if result != "" {
		t.Errorf("expected empty string for no diagnostics, got %q", result)
	}
}
