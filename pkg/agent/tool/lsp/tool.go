package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

var validOperations = []string{
	"diagnostics",
	"workspaceDiagnostics",
	"definition",
	"references",
	"implementation",
	"hover",
	"documentSymbol",
	"workspaceSymbol",
	"incomingCalls",
	"outgoingCalls",
}

// NewTool creates an LSP tool for coding agents.
func NewTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name:        "lsp",
		Description: prompt.LSP,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "string",
					"enum":        validOperations,
					"description": "The LSP operation to perform",
				},
				"file": map[string]any{
					"type":        "string",
					"description": "Path to the source file (not required for workspaceDiagnostics and workspaceSymbol)",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "Line number (1-based)",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "Column offset (1-based)",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Search query (only for workspaceSymbol)",
				},
			},
			"required": []string{"operation"},
		},
		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			operation, _ := args["operation"].(string)
			file, _ := args["file"].(string)
			query, _ := args["query"].(string)
			line := intArg(args, "line")
			column := intArg(args, "column")

			if !isValidOperation(operation) {
				return "", fmt.Errorf("invalid operation: %s", operation)
			}

			// Operations that don't need a file
			switch operation {
			case "workspaceDiagnostics":
				return execWorkspaceDiagnostics(ctx, manager)
			case "workspaceSymbol":
				return execWorkspaceSymbol(ctx, manager, query)
			}

			if file == "" {
				return "", fmt.Errorf("file is required for %s operation", operation)
			}

			// Operations that need position
			needsPosition := operation != "diagnostics" && operation != "documentSymbol"
			if needsPosition && (line == 0 || column == 0) {
				return "", fmt.Errorf("line and column are required for %s operation", operation)
			}

			if !filepath.IsAbs(file) {
				file = filepath.Join(manager.WorkingDir(), file)
			}

			if _, err := os.Stat(file); os.IsNotExist(err) {
				return "", fmt.Errorf("file not found: %s", file)
			}

			session, err := manager.GetSession(ctx, file)
			if err != nil {
				return "", err
			}

			uri, err := session.OpenDocument(ctx, file)
			if err != nil {
				return "", err
			}

			switch operation {
			case "diagnostics":
				return execDiagnostics(ctx, session, uri, file, manager.WorkingDir())
			case "definition":
				return execLocationOp(ctx, session, "textDocument/definition", "Definition", uri, line, column, manager.WorkingDir())
			case "references":
				return execReferences(ctx, session, uri, line, column, manager.WorkingDir())
			case "implementation":
				return execLocationOp(ctx, session, "textDocument/implementation", "Implementations", uri, line, column, manager.WorkingDir())
			case "hover":
				return execHover(ctx, session, uri, line, column)
			case "documentSymbol":
				return execDocumentSymbol(ctx, session, uri, file, manager.WorkingDir())
			case "incomingCalls":
				return execCallHierarchy(ctx, session, uri, line, column, manager.WorkingDir(), true)
			case "outgoingCalls":
				return execCallHierarchy(ctx, session, uri, line, column, manager.WorkingDir(), false)
			default:
				return "", fmt.Errorf("unknown operation: %s", operation)
			}
		},
	}
}

func isValidOperation(op string) bool {
	return slices.Contains(validOperations, op)
}

func execDiagnostics(ctx context.Context, session *lsp.Session, uri string, filePath string, workingDir string) (string, error) {
	diags := collectDiagnostics(ctx, session, uri)
	if len(diags) == 0 {
		return "No diagnostics found", nil
	}

	return formatDiagnostics(diags, filePath, workingDir), nil
}

// execLocationOp handles definition, implementation, and similar operations
// that return Location or LocationLink responses.
func execLocationOp(ctx context.Context, session *lsp.Session, method string, title string, uri string, line, column int, workingDir string) (string, error) {
	params := lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line - 1, Character: column - 1},
	}

	var result json.RawMessage
	if err := session.CallAndAwait(ctx, method, params, &result); err != nil {
		return "", err
	}

	locations, err := parseLocationResponse(result)
	if err != nil {
		return "", err
	}

	if len(locations) == 0 {
		return fmt.Sprintf("No %s found", strings.ToLower(title)), nil
	}

	return formatLocations(title, locations, workingDir), nil
}

func execReferences(ctx context.Context, session *lsp.Session, uri string, line, column int, workingDir string) (string, error) {
	params := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line - 1, Character: column - 1},
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}

	var result json.RawMessage
	if err := session.CallAndAwait(ctx, "textDocument/references", params, &result); err != nil {
		return "", err
	}

	locations, err := parseLocationResponse(result)
	if err != nil {
		return "", err
	}

	if len(locations) == 0 {
		return "No references found", nil
	}

	return formatLocations("References", locations, workingDir), nil
}

func execHover(ctx context.Context, session *lsp.Session, uri string, line, column int) (string, error) {
	params := lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line - 1, Character: column - 1},
	}

	var result json.RawMessage
	if err := session.CallAndAwait(ctx, "textDocument/hover", params, &result); err != nil {
		return "", err
	}

	if result == nil || string(result) == "null" {
		return "No hover information available", nil
	}

	var hover lsp.Hover
	if err := json.Unmarshal(result, &hover); err != nil {
		return "", err
	}

	if hover.Contents.Value == "" {
		return "No hover information available", nil
	}

	return hover.Contents.Value, nil
}

func execDocumentSymbol(ctx context.Context, session *lsp.Session, uri string, filePath string, workingDir string) (string, error) {
	params := lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	if err := session.CallAndAwait(ctx, "textDocument/documentSymbol", params, &result); err != nil {
		return "", err
	}

	if result == nil || string(result) == "null" {
		return "No symbols found", nil
	}

	// Try SymbolInformation[] first — check for location.uri which is unique to it
	var symInfos []lsp.SymbolInformation
	if err := json.Unmarshal(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
		return formatSymbolInformations(symInfos, workingDir), nil
	}

	// Fall back to DocumentSymbol[] (hierarchical, has selectionRange but no location)
	var docSymbols []lsp.DocumentSymbol
	if err := json.Unmarshal(result, &docSymbols); err == nil && len(docSymbols) > 0 {
		return formatDocumentSymbols(docSymbols, filePath, workingDir, 0), nil
	}

	return "No symbols found", nil
}

func execWorkspaceSymbol(ctx context.Context, manager *lsp.Manager, query string) (string, error) {
	servers := lsp.DetectServers(manager.WorkingDir())
	if len(servers) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	var allSymInfos []lsp.SymbolInformation
	var allWsSymbols []lsp.WorkspaceSymbol

	for _, server := range servers {
		session, err := manager.GetSessionByServer(ctx, server)
		if err != nil {
			continue
		}

		params := lsp.WorkspaceSymbolParams{
			Query: query,
		}

		var result json.RawMessage
		if err := session.CallAndAwait(ctx, "workspace/symbol", params, &result); err != nil || result == nil || string(result) == "null" {
			continue
		}

		// Try SymbolInformation[] first (has location.uri with range)
		var symInfos []lsp.SymbolInformation
		if err := json.Unmarshal(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
			allSymInfos = append(allSymInfos, symInfos...)
			continue
		}

		// Fall back to WorkspaceSymbol[] (location range may be omitted)
		var wsSymbols []lsp.WorkspaceSymbol
		if err := json.Unmarshal(result, &wsSymbols); err == nil {
			allWsSymbols = append(allWsSymbols, wsSymbols...)
		}
	}

	if len(allSymInfos) > 0 {
		return formatSymbolInformations(allSymInfos, manager.WorkingDir()), nil
	}

	if len(allWsSymbols) > 0 {
		return formatWorkspaceSymbols(allWsSymbols, manager.WorkingDir()), nil
	}

	return "No symbols found", nil
}

func execWorkspaceDiagnostics(ctx context.Context, manager *lsp.Manager) (string, error) {
	servers := lsp.DetectServers(manager.WorkingDir())
	if len(servers) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	var sb strings.Builder
	totalDiags := 0

	for _, server := range servers {
		session, err := manager.GetSessionByServer(ctx, server)
		if err != nil {
			continue
		}

		files := discoverSourceFiles(manager.WorkingDir(), server.Languages, 50)

		for _, file := range files {
			uri, err := session.OpenDocument(ctx, file)
			if err != nil {
				continue
			}

			diags := collectDiagnostics(ctx, session, uri)
			if len(diags) == 0 {
				continue
			}

			displayPath := relPath(manager.WorkingDir(), file)

			for _, diag := range diags {
				totalDiags++
				fmt.Fprintf(&sb, "  %s:%d:%d %s: %s\n", displayPath, diag.Range.Start.Line+1, diag.Range.Start.Character+1, diagnosticSeverityName(diag.Severity), diag.Message)
			}
		}
	}

	if totalDiags == 0 {
		return "No workspace diagnostics found", nil
	}

	return fmt.Sprintf("Workspace Diagnostics (%d found):\n%s", totalDiags, sb.String()), nil
}

// collectDiagnostics retrieves diagnostics for a single document URI.
func collectDiagnostics(ctx context.Context, session *lsp.Session, uri string) []lsp.Diagnostic {
	params := lsp.DocumentDiagnosticParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	if err := session.CallAndAwait(ctx, "textDocument/diagnostic", params, &result); err != nil || result == nil || string(result) == "null" {
		return nil
	}

	var report lsp.FullDocumentDiagnosticReport
	if err := json.Unmarshal(result, &report); err == nil && len(report.Items) > 0 {
		return report.Items
	}

	var diagnostics []lsp.Diagnostic
	if err := json.Unmarshal(result, &diagnostics); err == nil {
		return diagnostics
	}

	return nil
}

// execCallHierarchy handles both incomingCalls and outgoingCalls.
func execCallHierarchy(ctx context.Context, session *lsp.Session, uri string, line, column int, workingDir string, incoming bool) (string, error) {
	prepareParams := lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line - 1, Character: column - 1},
	}

	var prepareResult json.RawMessage
	if err := session.CallAndAwait(ctx, "textDocument/prepareCallHierarchy", prepareParams, &prepareResult); err != nil {
		return "", err
	}

	items, err := parseCallHierarchyItems(prepareResult)
	if err != nil || len(items) == 0 {
		return "No call hierarchy item found at this position", nil
	}

	if incoming {
		return execIncomingCalls(ctx, session, items[0], workingDir)
	}
	return execOutgoingCalls(ctx, session, items[0], workingDir)
}

func execIncomingCalls(ctx context.Context, session *lsp.Session, item lsp.CallHierarchyItem, workingDir string) (string, error) {
	params := lsp.CallHierarchyIncomingCallsParams{Item: item}

	var result json.RawMessage
	if err := session.CallAndAwait(ctx, "callHierarchy/incomingCalls", params, &result); err != nil {
		return "", err
	}

	var calls []lsp.CallHierarchyIncomingCall
	if err := unmarshalResult(result, &calls); err != nil {
		return "", err
	}

	if len(calls) == 0 {
		return "No incoming calls found", nil
	}

	return formatIncomingCalls(calls, workingDir), nil
}

func execOutgoingCalls(ctx context.Context, session *lsp.Session, item lsp.CallHierarchyItem, workingDir string) (string, error) {
	params := lsp.CallHierarchyOutgoingCallsParams{Item: item}

	var result json.RawMessage
	if err := session.CallAndAwait(ctx, "callHierarchy/outgoingCalls", params, &result); err != nil {
		return "", err
	}

	var calls []lsp.CallHierarchyOutgoingCall
	if err := unmarshalResult(result, &calls); err != nil {
		return "", err
	}

	if len(calls) == 0 {
		return "No outgoing calls found", nil
	}

	return formatOutgoingCalls(calls, workingDir), nil
}

func discoverSourceFiles(workingDir string, extensions []string, maxFiles int) []string {
	extSet := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		extSet["."+ext] = true
	}

	var files []string
	filepath.Walk(workingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__" || name == "target" || name == "build" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}

		if extSet[filepath.Ext(path)] {
			files = append(files, path)
			if len(files) >= maxFiles {
				return filepath.SkipAll
			}
		}

		return nil
	})

	return files
}

// --- Utilities ---

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

func relPath(workingDir, path string) string {
	if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func unmarshalResult(data json.RawMessage, v any) error {
	if data == nil || string(data) == "null" {
		return nil
	}
	return json.Unmarshal(data, v)
}

func uriToPath(uri string) string {
	if after, ok := strings.CutPrefix(uri, "file://"); ok {
		return after
	}
	return uri
}

func parseLocationResponse(data json.RawMessage) ([]lsp.Location, error) {
	if data == nil || string(data) == "null" {
		return nil, nil
	}

	// Try single Location
	var loc lsp.Location
	if err := json.Unmarshal(data, &loc); err == nil && loc.URI != "" {
		return []lsp.Location{loc}, nil
	}

	// Try Location[]
	var locs []lsp.Location
	if err := json.Unmarshal(data, &locs); err == nil && len(locs) > 0 && locs[0].URI != "" {
		return locs, nil
	}
	locs = nil

	// Try LocationLink[]
	var links []struct {
		TargetURI            string    `json:"targetUri"`
		TargetRange          lsp.Range `json:"targetRange"`
		TargetSelectionRange lsp.Range `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(data, &links); err == nil && len(links) > 0 && links[0].TargetURI != "" {
		for _, link := range links {
			locs = append(locs, lsp.Location{
				URI:   link.TargetURI,
				Range: link.TargetSelectionRange,
			})
		}
		return locs, nil
	}

	return nil, fmt.Errorf("unexpected location response format")
}

func parseCallHierarchyItems(data json.RawMessage) ([]lsp.CallHierarchyItem, error) {
	if data == nil || string(data) == "null" {
		return nil, nil
	}

	var items []lsp.CallHierarchyItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	return items, nil
}

func formatLocations(title string, locations []lsp.Location, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%d found):\n", title, len(locations))

	for _, loc := range locations {
		path := relPath(workingDir, uriToPath(loc.URI))
		fmt.Fprintf(&sb, "  %s:%d:%d\n", path, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
	}

	return sb.String()
}

func formatDiagnostics(diagnostics []lsp.Diagnostic, filePath string, workingDir string) string {
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

func formatDocumentSymbols(symbols []lsp.DocumentSymbol, filePath string, workingDir string, indent int) string {
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

func formatSymbolInformations(symbols []lsp.SymbolInformation, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Symbols (%d found):\n", len(symbols))

	for _, sym := range symbols {
		path := relPath(workingDir, uriToPath(sym.Location.URI))
		fmt.Fprintf(&sb, "  %s (%s) - %s:%d\n", sym.Name, symbolKindName(sym.Kind), path, sym.Location.Range.Start.Line+1)
	}

	return sb.String()
}

func formatWorkspaceSymbols(symbols []lsp.WorkspaceSymbol, workingDir string) string {
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

func formatIncomingCalls(calls []lsp.CallHierarchyIncomingCall, workingDir string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Incoming Calls (%d found):\n", len(calls))

	for _, c := range calls {
		path := relPath(workingDir, uriToPath(c.From.URI))
		fmt.Fprintf(&sb, "  %s (%s) - %s:%d\n", c.From.Name, symbolKindName(c.From.Kind), path, c.From.SelectionRange.Start.Line+1)
	}

	return sb.String()
}

func formatOutgoingCalls(calls []lsp.CallHierarchyOutgoingCall, workingDir string) string {
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

func diagnosticSeverityName(severity int) string {
	switch severity {
	case 1:
		return "Error"
	case 2:
		return "Warning"
	case 3:
		return "Info"
	case 4:
		return "Hint"
	default:
		return "Unknown"
	}
}
