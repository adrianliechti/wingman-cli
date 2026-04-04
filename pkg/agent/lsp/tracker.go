package lsp

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

const maxDiagnosticsPerFile = 10

// DiagnosticTracker tracks baselines and deduplicates diagnostics across turns.
type DiagnosticTracker struct {
	// baselines stores the diagnostic keys present before a file was edited.
	baselines map[string]map[string]struct{} // uri -> set of diagnostic keys

	// delivered tracks diagnostic keys already shown to the model, keyed by URI.
	delivered map[string]map[string]struct{}

	mu sync.Mutex
}

// NewDiagnosticTracker creates a new tracker.
func NewDiagnosticTracker() *DiagnosticTracker {
	return &DiagnosticTracker{
		baselines: make(map[string]map[string]struct{}),
		delivered: make(map[string]map[string]struct{}),
	}
}

// SetBaseline records the current diagnostics as a baseline before a file is edited.
// Only new diagnostics (not in the baseline) will be shown after the edit.
func (t *DiagnosticTracker) SetBaseline(uri string, diags []Diagnostic) {
	keys := make(map[string]struct{}, len(diags))
	for _, d := range diags {
		keys[diagnosticKey(d)] = struct{}{}
	}

	t.mu.Lock()
	t.baselines[uri] = keys
	// Clear delivered diagnostics for this file so fresh ones come through
	delete(t.delivered, uri)
	t.mu.Unlock()
}

// FilterNew returns only diagnostics that are new — not in the baseline and
// not previously delivered. It also applies volume limiting and severity sorting.
func (t *DiagnosticTracker) FilterNew(uri string, diags []Diagnostic) []Diagnostic {
	t.mu.Lock()
	baseline := t.baselines[uri]
	deliveredSet := t.delivered[uri]
	t.mu.Unlock()

	var filtered []Diagnostic
	for _, d := range diags {
		key := diagnosticKey(d)

		// Skip diagnostics that existed before the edit
		if baseline != nil {
			if _, inBaseline := baseline[key]; inBaseline {
				continue
			}
		}

		// Skip diagnostics already delivered in a previous turn
		if deliveredSet != nil {
			if _, wasDelivered := deliveredSet[key]; wasDelivered {
				continue
			}
		}

		filtered = append(filtered, d)
	}

	// Sort by severity (errors first)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Severity < filtered[j].Severity
	})

	// Cap per file
	if len(filtered) > maxDiagnosticsPerFile {
		filtered = filtered[:maxDiagnosticsPerFile]
	}

	return filtered
}

// MarkDelivered records that these diagnostics have been shown to the model.
func (t *DiagnosticTracker) MarkDelivered(uri string, diags []Diagnostic) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.delivered[uri] == nil {
		t.delivered[uri] = make(map[string]struct{})
	}

	for _, d := range diags {
		t.delivered[uri][diagnosticKey(d)] = struct{}{}
	}
}

// diagnosticKey creates a unique key for deduplication based on location + message.
func diagnosticKey(d Diagnostic) string {
	return fmt.Sprintf("%d:%d:%d:%s", d.Range.Start.Line, d.Range.Start.Character, d.Severity, d.Message)
}

// FormatNewDiagnostics formats only new diagnostics, with severity symbols and volume limiting.
func FormatNewDiagnostics(diagnostics []Diagnostic, filePath string, workingDir string) string {
	if len(diagnostics) == 0 {
		return ""
	}

	var sb strings.Builder

	displayPath := relPath(workingDir, filePath)

	fmt.Fprintf(&sb, "%s:\n", displayPath)
	for _, diag := range diagnostics {
		symbol := severitySymbol(diag.Severity)
		code := ""
		if diag.Code != nil {
			code = fmt.Sprintf(" [%v]", diag.Code)
		}
		source := ""
		if diag.Source != "" {
			source = fmt.Sprintf(" (%s)", diag.Source)
		}
		fmt.Fprintf(&sb, "  %s [Line %d:%d] %s%s%s\n",
			symbol,
			diag.Range.Start.Line+1,
			diag.Range.Start.Character+1,
			diag.Message,
			code,
			source,
		)
	}

	return sb.String()
}

func severitySymbol(severity int) string {
	switch severity {
	case DiagnosticSeverityError:
		return "✘"
	case DiagnosticSeverityWarning:
		return "⚠"
	case DiagnosticSeverityInformation:
		return "ℹ"
	case DiagnosticSeverityHint:
		return "★"
	default:
		return "•"
	}
}
