package lsp

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	maxListedLocations     = 100
	maxListedSymbols       = 100
	maxDefinitionSnippets  = 5
	definitionSnippetLines = 8
)

func relPath(workingDir, path string) string {
	if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

type sourceCache map[string][]string

func (c sourceCache) lines(path string) []string {
	if lines, ok := c[path]; ok {
		return lines
	}
	lines, err := readLines(path)
	if err != nil {
		lines = nil
	}
	c[path] = lines
	return lines
}

func (c sourceCache) locationLine(loc Location) (string, int, bool) {
	lines := c.lines(uriToPath(loc.URI))
	idx := loc.Range.Start.Line
	if idx < 0 || idx >= len(lines) {
		return "", loc.Range.Start.Character + 1, false
	}
	text := lines[idx]
	return text, runeColFromUTF16(text, loc.Range.Start.Character) + 1, true
}

func countFiles(locations []Location) int {
	seen := make(map[string]bool)
	for _, loc := range locations {
		seen[loc.URI] = true
	}
	return len(seen)
}

func formatLocations(title string, locations []Location, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%d found across %d files):\n", title, len(locations), countFiles(locations))

	cache := sourceCache{}
	shown := locations
	if len(shown) > maxListedLocations {
		shown = shown[:maxListedLocations]
	}

	for _, loc := range shown {
		path := relPath(workingDir, uriToPath(loc.URI))
		text, col, ok := cache.locationLine(loc)
		if ok {
			fmt.Fprintf(&sb, "%s:%d:%d: %s\n", path, loc.Range.Start.Line+1, col, strings.TrimSpace(text))
		} else {
			fmt.Fprintf(&sb, "%s:%d:%d\n", path, loc.Range.Start.Line+1, col)
		}
	}

	if len(locations) > len(shown) {
		fmt.Fprintf(&sb, "... showing first %d of %d locations\n", len(shown), len(locations))
	}

	return sb.String()
}

func formatDefinitions(locations []Location, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Definition (%d found):\n", len(locations))

	cache := sourceCache{}
	shown := locations
	if len(shown) > maxListedLocations {
		shown = shown[:maxListedLocations]
	}

	for i, loc := range shown {
		path := uriToPath(loc.URI)
		_, col, ok := cache.locationLine(loc)
		fmt.Fprintf(&sb, "\n%s:%d:%d\n", relPath(workingDir, path), loc.Range.Start.Line+1, col)

		if i >= maxDefinitionSnippets || !ok {
			continue
		}

		lines := cache.lines(path)
		start := loc.Range.Start.Line
		end := min(start+definitionSnippetLines, len(lines))
		for j := start; j < end; j++ {
			fmt.Fprintf(&sb, "%6d\t%s\n", j+1, lines[j])
		}
	}

	if len(locations) > len(shown) {
		fmt.Fprintf(&sb, "\n... showing first %d of %d locations\n", len(shown), len(locations))
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
	total := len(symbols)
	if len(symbols) > maxListedSymbols {
		symbols = symbols[:maxListedSymbols]
	}

	files := groupByPath(symbols, func(s SymbolInformation) string {
		return relPath(workingDir, uriToPath(s.Location.URI))
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "Symbols (%d found across %d files):\n", total, len(files))

	for _, file := range files {
		fmt.Fprintf(&sb, "\n%s:\n", file.Path)
		for _, sym := range file.Items {
			fmt.Fprintf(&sb, "  %s (%s) - Line %d\n", sym.Name, symbolKindName(sym.Kind), sym.Location.Range.Start.Line+1)
		}
	}

	if total > len(symbols) {
		fmt.Fprintf(&sb, "\n... showing first %d of %d symbols; narrow the query for more\n", len(symbols), total)
	}

	return sb.String()
}

func formatWorkspaceSymbols(symbols []WorkspaceSymbol, workingDir string) string {
	total := len(symbols)
	if len(symbols) > maxListedSymbols {
		symbols = symbols[:maxListedSymbols]
	}

	files := groupByPath(symbols, func(s WorkspaceSymbol) string {
		return relPath(workingDir, uriToPath(s.Location.URI))
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "Symbols (%d found across %d files):\n", total, len(files))

	for _, file := range files {
		fmt.Fprintf(&sb, "\n%s:\n", file.Path)
		for _, sym := range file.Items {
			if sym.Location.Range != nil {
				fmt.Fprintf(&sb, "  %s (%s) - Line %d\n", sym.Name, symbolKindName(sym.Kind), sym.Location.Range.Start.Line+1)
			} else {
				fmt.Fprintf(&sb, "  %s (%s)\n", sym.Name, symbolKindName(sym.Kind))
			}
		}
	}

	if total > len(symbols) {
		fmt.Fprintf(&sb, "\n... showing first %d of %d symbols; narrow the query for more\n", len(symbols), total)
	}

	return sb.String()
}

type pathGroup[T any] struct {
	Path  string
	Items []T
}

func groupByPath[T any](items []T, pathOf func(T) string) []pathGroup[T] {
	indexes := make(map[string]int)
	var groups []pathGroup[T]

	for _, item := range items {
		path := pathOf(item)
		idx, ok := indexes[path]
		if !ok {
			idx = len(groups)
			indexes[path] = idx
			groups = append(groups, pathGroup[T]{Path: path})
		}
		groups[idx].Items = append(groups[idx].Items, item)
	}

	return groups
}

func formatRangeLines(ranges []Range) string {
	if len(ranges) == 0 {
		return ""
	}

	var lines []string
	seen := make(map[int]bool)
	for _, r := range ranges {
		line := r.Start.Line + 1
		if seen[line] {
			continue
		}
		seen[line] = true
		lines = append(lines, fmt.Sprintf("%d", line))
	}

	return strings.Join(lines, ", ")
}

func formatIncomingCalls(calls []CallHierarchyIncomingCall, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Incoming Calls (%d found):\n", len(calls))

	for _, c := range calls {
		path := relPath(workingDir, uriToPath(c.From.URI))
		sites := ""
		if s := formatRangeLines(c.FromRanges); s != "" {
			sites = ", call sites at lines " + s
		}
		fmt.Fprintf(&sb, "  %s (%s) - %s:%d%s\n", c.From.Name, symbolKindName(c.From.Kind), path, c.From.SelectionRange.Start.Line+1, sites)
	}

	return sb.String()
}

func formatOutgoingCalls(calls []CallHierarchyOutgoingCall, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Outgoing Calls (%d found):\n", len(calls))

	for _, c := range calls {
		path := relPath(workingDir, uriToPath(c.To.URI))
		sites := ""
		if s := formatRangeLines(c.FromRanges); s != "" {
			sites = ", called at lines " + s
		}
		fmt.Fprintf(&sb, "  %s (%s) - %s:%d%s\n", c.To.Name, symbolKindName(c.To.Kind), path, c.To.SelectionRange.Start.Line+1, sites)
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

func formatDiagnosticLine(displayPath string, diag Diagnostic) string {
	source := ""
	if diag.Source != "" {
		source = fmt.Sprintf("[%s] ", diag.Source)
	}
	return fmt.Sprintf("%s:%d:%d %s: %s%s", displayPath, diag.Range.Start.Line+1, diag.Range.Start.Character+1, DiagnosticSeverityName(diag.Severity), source, diag.Message)
}

func FormatDiagnostics(diagnostics []Diagnostic, filePath string, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Diagnostics (%d found):\n", len(diagnostics))

	displayPath := relPath(workingDir, filePath)

	for _, diag := range diagnostics {
		fmt.Fprintf(&sb, "  %s\n", formatDiagnosticLine(displayPath, diag))
	}

	return sb.String()
}

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
