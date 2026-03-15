package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}

// relPath converts an absolute path to a relative path from workingDir, if possible.
func relPath(workingDir, path string) string {
	if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// unmarshalResult unmarshals a JSON-RPC result, returning nil for null/empty results.
func unmarshalResult(data json.RawMessage, v any) error {
	if data == nil || string(data) == "null" {
		return nil
	}
	return json.Unmarshal(data, v)
}

func parseLocationResponse(data json.RawMessage) ([]Location, error) {
	if data == nil || string(data) == "null" {
		return nil, nil
	}

	// Try single Location
	var loc Location
	if err := json.Unmarshal(data, &loc); err == nil && loc.URI != "" {
		return []Location{loc}, nil
	}

	// Try Location[]
	var locs []Location
	if err := json.Unmarshal(data, &locs); err == nil && len(locs) > 0 && locs[0].URI != "" {
		return locs, nil
	}
	locs = nil

	// Try LocationLink[]
	var links []struct {
		TargetURI            string `json:"targetUri"`
		TargetRange          Range  `json:"targetRange"`
		TargetSelectionRange Range  `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(data, &links); err == nil && len(links) > 0 && links[0].TargetURI != "" {
		for _, link := range links {
			locs = append(locs, Location{
				URI:   link.TargetURI,
				Range: link.TargetSelectionRange,
			})
		}
		return locs, nil
	}

	return nil, fmt.Errorf("unexpected location response format")
}

func parseCallHierarchyItems(data json.RawMessage) ([]CallHierarchyItem, error) {
	if data == nil || string(data) == "null" {
		return nil, nil
	}

	var items []CallHierarchyItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	return items, nil
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

func formatDiagnostics(diagnostics []Diagnostic, filePath string, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Diagnostics (%d found):\n", len(diagnostics))

	displayPath := relPath(workingDir, filePath)

	for _, diag := range diagnostics {
		source := ""
		if diag.Source != "" {
			source = fmt.Sprintf("[%s] ", diag.Source)
		}
		fmt.Fprintf(&sb, "  %s:%d:%d %s: %s%s\n", displayPath, diag.Range.Start.Line+1, diag.Range.Start.Character+1, diagnosticSeverityName(diag.Severity), source, diag.Message)
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
