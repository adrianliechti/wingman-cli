package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/language"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

const (
	maxListedLocations     = 100
	maxListedSymbols       = 100
	maxDefinitionSnippets  = 5
	definitionSnippetLines = 8
)

func relativePath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return path
}

type sourceCache map[string][]string

func (c sourceCache) lines(path string) []string {
	if lines, ok := c[path]; ok {
		return lines
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	c[path] = lines
	return lines
}

func (c sourceCache) locationLine(location lsp.Location) (string, int, bool) {
	path, ok := fileuri.Path(location.URI.String())
	if !ok {
		return "", int(location.Range.Start.Character) + 1, false
	}
	lines := c.lines(path)
	line := int(location.Range.Start.Line)
	if line < 0 || line >= len(lines) {
		return "", int(location.Range.Start.Character) + 1, false
	}
	return lines[line], runeColumnFromUTF16(lines[line], int(location.Range.Start.Character)) + 1, true
}

func formatLocations(title string, locations []lsp.Location, root string) string {
	files := make(map[string]bool)
	for _, location := range locations {
		files[location.URI.String()] = true
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s (%d found across %d files):\n", title, len(locations), len(files))
	shown := locations
	if len(shown) > maxListedLocations {
		shown = shown[:maxListedLocations]
	}
	cache := sourceCache{}
	for _, location := range shown {
		path, _ := fileuri.Path(location.URI.String())
		text, column, found := cache.locationLine(location)
		if found {
			fmt.Fprintf(&out, "%s:%d:%d: %s\n", relativePath(root, path), location.Range.Start.Line+1, column, strings.TrimSpace(text))
		} else {
			fmt.Fprintf(&out, "%s:%d:%d\n", relativePath(root, path), location.Range.Start.Line+1, column)
		}
	}
	if len(locations) > len(shown) {
		fmt.Fprintf(&out, "... showing first %d of %d locations\n", len(shown), len(locations))
	}
	return out.String()
}

func formatDefinitions(locations []lsp.Location, root string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Definition (%d found):\n", len(locations))
	shown := locations
	if len(shown) > maxListedLocations {
		shown = shown[:maxListedLocations]
	}
	cache := sourceCache{}
	for i, location := range shown {
		path, _ := fileuri.Path(location.URI.String())
		_, column, found := cache.locationLine(location)
		fmt.Fprintf(&out, "\n%s:%d:%d\n", relativePath(root, path), location.Range.Start.Line+1, column)
		if i >= maxDefinitionSnippets || !found {
			continue
		}
		lines := cache.lines(path)
		start := int(location.Range.Start.Line)
		for line := start; line < min(start+definitionSnippetLines, len(lines)); line++ {
			fmt.Fprintf(&out, "%6d\t%s\n", line+1, lines[line])
		}
	}
	if len(locations) > len(shown) {
		fmt.Fprintf(&out, "\n... showing first %d of %d locations\n", len(shown), len(locations))
	}
	return out.String()
}

func formatDocumentSymbols(symbols []lsp.DocumentSymbol, filePath, root string, indent int) string {
	var out strings.Builder
	if indent == 0 {
		fmt.Fprintf(&out, "Symbols in %s:\n", relativePath(root, filePath))
	}
	prefix := strings.Repeat("  ", indent+1)
	for _, symbol := range symbols {
		detail := ""
		if symbol.Detail != nil && *symbol.Detail != "" {
			detail = " " + *symbol.Detail
		}
		fmt.Fprintf(&out, "%s%s (%s)%s - line %d\n", prefix, symbol.Name, symbolKindName(symbol.Kind), detail, symbol.SelectionRange.Start.Line+1)
		if len(symbol.Children) > 0 {
			out.WriteString(formatDocumentSymbols(symbol.Children, filePath, root, indent+1))
		}
	}
	return out.String()
}

func formatSymbolInformation(symbols []lsp.SymbolInformation, root string) string {
	return formatSymbols(len(symbols), root, symbols, func(symbol lsp.SymbolInformation) (string, string) {
		path, _ := fileuri.Path(symbol.Location.URI.String())
		return path, fmt.Sprintf("  %s (%s) - Line %d\n", symbol.Name, symbolKindName(symbol.Kind), symbol.Location.Range.Start.Line+1)
	})
}

func formatWorkspaceSymbols(symbols []lsp.WorkspaceSymbol, root string) string {
	return formatSymbols(len(symbols), root, symbols, func(symbol lsp.WorkspaceSymbol) (string, string) {
		switch location := symbol.Location.(type) {
		case *lsp.Location:
			path, _ := fileuri.Path(location.URI.String())
			return path, fmt.Sprintf("  %s (%s) - Line %d\n", symbol.Name, symbolKindName(symbol.Kind), location.Range.Start.Line+1)
		case *lsp.LocationURIOnly:
			path, _ := fileuri.Path(location.URI.String())
			return path, fmt.Sprintf("  %s (%s)\n", symbol.Name, symbolKindName(symbol.Kind))
		default:
			return "", fmt.Sprintf("  %s (%s)\n", symbol.Name, symbolKindName(symbol.Kind))
		}
	})
}

func formatSymbols[T any](total int, root string, symbols []T, line func(T) (string, string)) string {
	if len(symbols) > maxListedSymbols {
		symbols = symbols[:maxListedSymbols]
	}
	type group struct {
		path  string
		lines []string
	}
	indexes := make(map[string]int)
	var groups []group
	for _, symbol := range symbols {
		path, text := line(symbol)
		path = relativePath(root, path)
		index, ok := indexes[path]
		if !ok {
			index = len(groups)
			indexes[path] = index
			groups = append(groups, group{path: path})
		}
		groups[index].lines = append(groups[index].lines, text)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Symbols (%d found across %d files):\n", total, len(groups))
	for _, group := range groups {
		fmt.Fprintf(&out, "\n%s:\n", group.path)
		for _, line := range group.lines {
			out.WriteString(line)
		}
	}
	if total > len(symbols) {
		fmt.Fprintf(&out, "\n... showing first %d of %d symbols; narrow the query for more\n", len(symbols), total)
	}
	return out.String()
}

func formatIncomingCalls(calls []lsp.CallHierarchyIncomingCall, root string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Incoming Calls (%d found):\n", len(calls))
	for _, call := range calls {
		path, _ := fileuri.Path(call.From.URI.String())
		fmt.Fprintf(&out, "  %s (%s) - %s:%d%s\n", call.From.Name, symbolKindName(call.From.Kind), relativePath(root, path), call.From.SelectionRange.Start.Line+1, formatCallSites(call.FromRanges, ", call sites at lines "))
	}
	return out.String()
}

func formatOutgoingCalls(calls []lsp.CallHierarchyOutgoingCall, root string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Outgoing Calls (%d found):\n", len(calls))
	for _, call := range calls {
		path, _ := fileuri.Path(call.To.URI.String())
		fmt.Fprintf(&out, "  %s (%s) - %s:%d%s\n", call.To.Name, symbolKindName(call.To.Kind), relativePath(root, path), call.To.SelectionRange.Start.Line+1, formatCallSites(call.FromRanges, ", called at lines "))
	}
	return out.String()
}

func formatCallSites(ranges []lsp.Range, prefix string) string {
	seen := make(map[uint32]bool)
	var lines []string
	for _, value := range ranges {
		line := value.Start.Line + 1
		if !seen[line] {
			seen[line] = true
			lines = append(lines, fmt.Sprint(line))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return prefix + strings.Join(lines, ", ")
}

func formatWorkspaceDiagnostics(report language.WorkspaceReport, root string) string {
	type fileDiagnostic struct {
		path       string
		diagnostic language.Diagnostic
	}
	var all []fileDiagnostic
	for path, values := range report.Diagnostics {
		for _, value := range values {
			all = append(all, fileDiagnostic{relativePath(root, path), value})
		}
	}
	slices.SortStableFunc(all, func(a, b fileDiagnostic) int {
		return language.CompareSeverity(a.diagnostic.Severity, b.diagnostic.Severity)
	})
	var notes []string
	if report.DiscoveredFiles > report.CheckedFiles || report.DiscoveryTruncated {
		total := fmt.Sprint(report.DiscoveredFiles)
		if report.DiscoveryTruncated {
			total += "+"
		}
		notes = append(notes, fmt.Sprintf("checked %d of %s source files; results are partial", report.CheckedFiles, total))
	} else {
		notes = append(notes, fmt.Sprintf("checked %d source files", report.CheckedFiles))
	}
	if len(report.UnavailableServers) > 0 {
		notes = append(notes, "server unavailable: "+strings.Join(report.UnavailableServers, ", "))
	}
	if report.Analyzing {
		notes = append(notes, "a language server is still analyzing; rerun for final results")
	}
	coverage := "Coverage: " + strings.Join(notes, "; ")
	if len(all) == 0 {
		return "No workspace diagnostics found\n" + coverage
	}
	diagnosticValues := make([]language.Diagnostic, len(all))
	for i := range all {
		diagnosticValues[i] = all[i].diagnostic
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Workspace Diagnostics (%d found: %s):\n", len(all), language.DiagnosticSummary(diagnosticValues))
	for _, value := range all {
		fmt.Fprintf(&out, "  %s\n", language.FormatDiagnosticLine(value.path, value.diagnostic))
	}
	out.WriteString(coverage)
	return out.String()
}

var symbolKindNames = map[lsp.SymbolKind]string{
	lsp.SymbolKindFile: "File", lsp.SymbolKindModule: "Module",
	lsp.SymbolKindNamespace: "Namespace", lsp.SymbolKindPackage: "Package",
	lsp.SymbolKindClass: "Class", lsp.SymbolKindMethod: "Method",
	lsp.SymbolKindProperty: "Property", lsp.SymbolKindField: "Field",
	lsp.SymbolKindConstructor: "Constructor", lsp.SymbolKindEnum: "Enum",
	lsp.SymbolKindInterface: "Interface", lsp.SymbolKindFunction: "Function",
	lsp.SymbolKindVariable: "Variable", lsp.SymbolKindConstant: "Constant",
	lsp.SymbolKindString: "String", lsp.SymbolKindNumber: "Number",
	lsp.SymbolKindBoolean: "Boolean", lsp.SymbolKindArray: "Array",
	lsp.SymbolKindObject: "Object", lsp.SymbolKindKey: "Key",
	lsp.SymbolKindNull: "Null", lsp.SymbolKindEnumMember: "EnumMember",
	lsp.SymbolKindStruct: "Struct", lsp.SymbolKindEvent: "Event",
	lsp.SymbolKindOperator: "Operator", lsp.SymbolKindTypeParameter: "TypeParameter",
}

func symbolKindName(kind lsp.SymbolKind) string {
	if name := symbolKindNames[kind]; name != "" {
		return name
	}
	return "Symbol"
}
