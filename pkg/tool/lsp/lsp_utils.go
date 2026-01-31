package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// intArg extracts an integer argument from the args map.
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

// parseLocationResponse parses various location response formats from LSP.
func parseLocationResponse(data json.RawMessage) ([]Location, error) {
	if data == nil || string(data) == "null" {
		return nil, nil
	}

	// Try single Location
	var loc Location
	if err := json.Unmarshal(data, &loc); err == nil && loc.URI != "" {
		return []Location{loc}, nil
	}

	// Try []Location
	var locs []Location
	if err := json.Unmarshal(data, &locs); err == nil {
		return locs, nil
	}

	// Try []LocationLink
	var links []struct {
		TargetURI   string `json:"targetUri"`
		TargetRange Range  `json:"targetRange"`
	}
	if err := json.Unmarshal(data, &links); err == nil {
		for _, link := range links {
			locs = append(locs, Location{
				URI:   link.TargetURI,
				Range: link.TargetRange,
			})
		}
		return locs, nil
	}

	return nil, fmt.Errorf("unexpected location response format")
}

// formatLocations formats a list of locations for display.
func formatLocations(title string, locations []Location, workingDir string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s (%d found):\n", title, len(locations)))

	for _, loc := range locations {
		path := uriToPath(loc.URI)
		if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		sb.WriteString(fmt.Sprintf("  %s:%d:%d\n", path, loc.Range.Start.Line+1, loc.Range.Start.Character+1))
	}

	return sb.String()
}

// formatDocumentSymbols formats hierarchical document symbols for display.
func formatDocumentSymbols(symbols []DocumentSymbol, indent int) string {
	var sb strings.Builder
	prefix := strings.Repeat("  ", indent)

	for _, sym := range symbols {
		kind := symbolKindName(sym.Kind)
		line := sym.Range.Start.Line + 1
		sb.WriteString(fmt.Sprintf("%s%s %s (line %d)\n", prefix, kind, sym.Name, line))
		if len(sym.Children) > 0 {
			sb.WriteString(formatDocumentSymbols(sym.Children, indent+1))
		}
	}

	return sb.String()
}

// formatSymbolInformation formats flat symbol information for display.
func formatSymbolInformation(symbols []SymbolInformation) string {
	var sb strings.Builder

	for _, sym := range symbols {
		kind := symbolKindName(sym.Kind)
		line := sym.Location.Range.Start.Line + 1
		if sym.ContainerName != "" {
			sb.WriteString(fmt.Sprintf("%s %s.%s (line %d)\n", kind, sym.ContainerName, sym.Name, line))
		} else {
			sb.WriteString(fmt.Sprintf("%s %s (line %d)\n", kind, sym.Name, line))
		}
	}

	return sb.String()
}

// formatWorkspaceSymbols formats workspace symbols for display.
func formatWorkspaceSymbols(symbols []SymbolInformation, workingDir string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Workspace Symbols (%d found):\n", len(symbols)))

	for _, sym := range symbols {
		kind := symbolKindName(sym.Kind)
		path := uriToPath(sym.Location.URI)
		if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		line := sym.Location.Range.Start.Line + 1
		if sym.ContainerName != "" {
			sb.WriteString(fmt.Sprintf("  %s %s.%s (%s:%d)\n", kind, sym.ContainerName, sym.Name, path, line))
		} else {
			sb.WriteString(fmt.Sprintf("  %s %s (%s:%d)\n", kind, sym.Name, path, line))
		}
	}

	return sb.String()
}

// formatIncomingCalls formats incoming call hierarchy results.
func formatIncomingCalls(calls []CallHierarchyIncomingCall, workingDir string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Incoming Calls (%d found):\n", len(calls)))

	for _, call := range calls {
		path := uriToPath(call.From.URI)
		if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		kind := symbolKindName(call.From.Kind)
		line := call.From.Range.Start.Line + 1
		sb.WriteString(fmt.Sprintf("  %s %s (%s:%d)\n", kind, call.From.Name, path, line))
	}

	return sb.String()
}

// formatOutgoingCalls formats outgoing call hierarchy results.
func formatOutgoingCalls(calls []CallHierarchyOutgoingCall, workingDir string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Outgoing Calls (%d found):\n", len(calls)))

	for _, call := range calls {
		path := uriToPath(call.To.URI)
		if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		kind := symbolKindName(call.To.Kind)
		line := call.To.Range.Start.Line + 1
		sb.WriteString(fmt.Sprintf("  %s %s (%s:%d)\n", kind, call.To.Name, path, line))
	}

	return sb.String()
}
