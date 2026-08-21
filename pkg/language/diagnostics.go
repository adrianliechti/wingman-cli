package language

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Severity int

const (
	SeverityError       Severity = 1
	SeverityWarning     Severity = 2
	SeverityInformation Severity = 3
	SeverityHint        Severity = 4
)

type Diagnostic struct {
	Range    Range    `json:"range"`
	Severity Severity `json:"severity,omitempty"`
	Code     any      `json:"code,omitempty"`
	Source   string   `json:"source,omitempty"`
	Message  string   `json:"message"`
}

type WorkspaceReport struct {
	Diagnostics        map[string][]Diagnostic
	CheckedFiles       int
	DiscoveredFiles    int
	DiscoveryTruncated bool
	UnknownFiles       int
	UnavailableServers []string
	Analyzing          bool
}

func DiagnosticsFromProtocol(values []lsp.Diagnostic) []Diagnostic {
	result := make([]Diagnostic, 0, len(values))
	for _, value := range values {
		source, _ := value.Source.Get()
		message := ""
		switch value := value.Message.(type) {
		case lsp.String:
			message = string(value)
		case *lsp.MarkupContent:
			message = value.Value
		}
		result = append(result, Diagnostic{
			Range: Range{
				Start: Position{Line: int(value.Range.Start.Line), Character: int(value.Range.Start.Character)},
				End:   Position{Line: int(value.Range.End.Line), Character: int(value.Range.End.Character)},
			},
			Severity: Severity(value.Severity),
			Code:     value.Code,
			Source:   source,
			Message:  message,
		})
	}
	return result
}

func FormatDiagnosticLine(path string, diagnostic Diagnostic) string {
	source := ""
	if diagnostic.Source != "" {
		source = fmt.Sprintf("[%s] ", diagnostic.Source)
	}
	return fmt.Sprintf("%s:%d:%d %s: %s%s", path, diagnostic.Range.Start.Line+1, diagnostic.Range.Start.Character+1, SeverityName(diagnostic.Severity), source, diagnostic.Message)
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInformation:
		return 2
	case SeverityHint:
		return 3
	default:
		return 4
	}
}

func CompareSeverity(a, b Severity) int { return severityRank(a) - severityRank(b) }

func DiagnosticSummary(values []Diagnostic) string {
	counts := make(map[Severity]int)
	for _, value := range values {
		counts[value.Severity]++
	}
	var parts []string
	add := func(severity Severity, singular, plural string) {
		count := counts[severity]
		delete(counts, severity)
		if count == 0 {
			return
		}
		label := plural
		if count == 1 {
			label = singular
		}
		parts = append(parts, fmt.Sprintf("%d %s", count, label))
	}
	add(SeverityError, "error", "errors")
	add(SeverityWarning, "warning", "warnings")
	add(SeverityInformation, "info", "info")
	add(SeverityHint, "hint", "hints")
	other := 0
	for _, count := range counts {
		other += count
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("%d other", other))
	}
	return strings.Join(parts, ", ")
}

func FormatDiagnostics(values []Diagnostic, filePath, root string) string {
	sorted := slices.Clone(values)
	slices.SortStableFunc(sorted, func(a, b Diagnostic) int { return CompareSeverity(a.Severity, b.Severity) })
	var result strings.Builder
	fmt.Fprintf(&result, "Diagnostics (%d found: %s):\n", len(sorted), DiagnosticSummary(sorted))
	path := relativeDiagnosticPath(root, filePath)
	for _, diagnostic := range sorted {
		fmt.Fprintf(&result, "  %s\n", FormatDiagnosticLine(path, diagnostic))
	}
	return result.String()
}

func SeverityName(severity Severity) string {
	switch severity {
	case SeverityError:
		return "Error"
	case SeverityWarning:
		return "Warning"
	case SeverityInformation:
		return "Info"
	case SeverityHint:
		return "Hint"
	default:
		return "Unknown"
	}
}

func relativeDiagnosticPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return path
}
