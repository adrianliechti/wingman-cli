package lsp

import (
	"fmt"
	"path/filepath"
	"strings"
)

func relPath(workingDir, path string) string {
	if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func uriToPath(uri string) string {
	if after, ok := strings.CutPrefix(uri, "file://"); ok {
		return after
	}
	return uri
}

func formatLocations(title string, locations []Location, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%d found):\n", title, len(locations))

	for _, loc := range locations {
		path := relPath(workingDir, uriToPath(loc.URI))
		fmt.Fprintf(&sb, "  %s:%d:%d\n", path, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
	}

	return sb.String()
}

func formatDocumentSymbols(symbols []DocumentSymbol, filePath string, workingDir string, indent int) string {
	var sb strings.Builder

	if indent == 0 {
		fmt.Fprintf(&sb, "Symbols in %s:\n", relPath(workingDir, filePath))
	}

	prefix := strings.Repeat("  ", indent+1)

	for _, sym := range symbols {
		detail := ""
		if sym.Detail != "" {
			detail = " " + sym.Detail
		}
		fmt.Fprintf(&sb, "%s%s (%s)%s - line %d\n", prefix, sym.Name, symbolKindName(sym.Kind), detail, sym.SelectionRange.Start.Line+1)

		if len(sym.Children) > 0 {
			fmt.Fprint(&sb, formatDocumentSymbols(sym.Children, filePath, workingDir, indent+1))
		}
	}

	return sb.String()
}

func formatSymbolInformations(symbols []SymbolInformation, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Symbols (%d found):\n", len(symbols))

	for _, sym := range symbols {
		path := relPath(workingDir, uriToPath(sym.Location.URI))
		fmt.Fprintf(&sb, "  %s (%s) - %s:%d\n", sym.Name, symbolKindName(sym.Kind), path, sym.Location.Range.Start.Line+1)
	}

	return sb.String()
}

func formatWorkspaceSymbols(symbols []WorkspaceSymbol, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Symbols (%d found):\n", len(symbols))

	for _, sym := range symbols {
		path := relPath(workingDir, uriToPath(sym.Location.URI))
		if sym.Location.Range != nil {
			fmt.Fprintf(&sb, "  %s (%s) - %s:%d\n", sym.Name, symbolKindName(sym.Kind), path, sym.Location.Range.Start.Line+1)
		} else {
			fmt.Fprintf(&sb, "  %s (%s) - %s\n", sym.Name, symbolKindName(sym.Kind), path)
		}
	}

	return sb.String()
}

func formatIncomingCalls(calls []CallHierarchyIncomingCall, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Incoming Calls (%d found):\n", len(calls))

	for _, c := range calls {
		path := relPath(workingDir, uriToPath(c.From.URI))
		fmt.Fprintf(&sb, "  %s (%s) - %s:%d\n", c.From.Name, symbolKindName(c.From.Kind), path, c.From.SelectionRange.Start.Line+1)
	}

	return sb.String()
}

func formatOutgoingCalls(calls []CallHierarchyOutgoingCall, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Outgoing Calls (%d found):\n", len(calls))

	for _, c := range calls {
		path := relPath(workingDir, uriToPath(c.To.URI))
		fmt.Fprintf(&sb, "  %s (%s) - %s:%d\n", c.To.Name, symbolKindName(c.To.Kind), path, c.To.SelectionRange.Start.Line+1)
	}

	return sb.String()
}

var symbolKindNames = [...]string{
	1:  "File",
	2:  "Module",
	3:  "Namespace",
	4:  "Package",
	5:  "Class",
	6:  "Method",
	7:  "Property",
	8:  "Field",
	9:  "Constructor",
	10: "Enum",
	11: "Interface",
	12: "Function",
	13: "Variable",
	14: "Constant",
	15: "String",
	16: "Number",
	17: "Boolean",
	18: "Array",
	19: "Object",
	20: "Key",
	21: "Null",
	22: "EnumMember",
	23: "Struct",
	24: "Event",
	25: "Operator",
	26: "TypeParameter",
}

func symbolKindName(kind int) string {
	if kind >= 1 && kind < len(symbolKindNames) && symbolKindNames[kind] != "" {
		return symbolKindNames[kind]
	}
	return "Symbol"
}

// FormatDiagnostics formats diagnostics into a human-readable string.
func FormatDiagnostics(diagnostics []Diagnostic, filePath string, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Diagnostics (%d found):\n", len(diagnostics))

	displayPath := relPath(workingDir, filePath)

	for _, diag := range diagnostics {
		source := ""
		if diag.Source != "" {
			source = fmt.Sprintf("[%s] ", diag.Source)
		}
		fmt.Fprintf(&sb, "  %s:%d:%d %s: %s%s\n", displayPath, diag.Range.Start.Line+1, diag.Range.Start.Character+1, DiagnosticSeverityName(diag.Severity), source, diag.Message)
	}

	return sb.String()
}

// DiagnosticSeverityName returns the human-readable name for a diagnostic severity.
func DiagnosticSeverityName(severity int) string {
	switch severity {
	case DiagnosticSeverityError:
		return "Error"
	case DiagnosticSeverityWarning:
		return "Warning"
	case DiagnosticSeverityInformation:
		return "Info"
	case DiagnosticSeverityHint:
		return "Hint"
	default:
		return "Unknown"
	}
}
