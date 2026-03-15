package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/tool"
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
func NewTool(manager *Manager) tool.Tool {
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
				file = filepath.Join(manager.workingDir, file)
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
				return execDiagnostics(ctx, session, uri, file, manager.workingDir)
			case "definition":
				return execLocationOp(ctx, session, "textDocument/definition", "Definition", uri, line, column, manager.workingDir)
			case "references":
				return execReferences(ctx, session, uri, line, column, manager.workingDir)
			case "implementation":
				return execLocationOp(ctx, session, "textDocument/implementation", "Implementations", uri, line, column, manager.workingDir)
			case "hover":
				return execHover(ctx, session, uri, line, column)
			case "documentSymbol":
				return execDocumentSymbol(ctx, session, uri, file, manager.workingDir)
			case "incomingCalls":
				return execCallHierarchy(ctx, session, uri, line, column, manager.workingDir, true)
			case "outgoingCalls":
				return execCallHierarchy(ctx, session, uri, line, column, manager.workingDir, false)
			default:
				return "", fmt.Errorf("unknown operation: %s", operation)
			}
		},
	}
}

func isValidOperation(op string) bool {
	return slices.Contains(validOperations, op)
}

func execDiagnostics(ctx context.Context, session *Session, uri string, filePath string, workingDir string) (string, error) {
	diags := collectDiagnostics(ctx, session, uri)
	if len(diags) == 0 {
		return "No diagnostics found", nil
	}

	return formatDiagnostics(diags, filePath, workingDir), nil
}

// execLocationOp handles definition, implementation, and similar operations
// that return Location or LocationLink responses.
func execLocationOp(ctx context.Context, session *Session, method string, title string, uri string, line, column int, workingDir string) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, method, params)
	if err := call.Await(ctx, &result); err != nil {
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

func execReferences(ctx context.Context, session *Session, uri string, line, column int, workingDir string) (string, error) {
	params := ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
		Context:      ReferenceContext{IncludeDeclaration: true},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/references", params)
	if err := call.Await(ctx, &result); err != nil {
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

func execHover(ctx context.Context, session *Session, uri string, line, column int) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/hover", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", err
	}

	if result == nil || string(result) == "null" {
		return "No hover information available", nil
	}

	var hover Hover
	if err := json.Unmarshal(result, &hover); err != nil {
		return "", err
	}

	if hover.Contents.Value == "" {
		return "No hover information available", nil
	}

	return hover.Contents.Value, nil
}

func execDocumentSymbol(ctx context.Context, session *Session, uri string, filePath string, workingDir string) (string, error) {
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/documentSymbol", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", err
	}

	if result == nil || string(result) == "null" {
		return "No symbols found", nil
	}

	// Try SymbolInformation[] first — check for location.uri which is unique to it
	var symInfos []SymbolInformation
	if err := json.Unmarshal(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
		return formatSymbolInformations(symInfos, workingDir), nil
	}

	// Fall back to DocumentSymbol[] (hierarchical, has selectionRange but no location)
	var docSymbols []DocumentSymbol
	if err := json.Unmarshal(result, &docSymbols); err == nil && len(docSymbols) > 0 {
		return formatDocumentSymbols(docSymbols, filePath, workingDir, 0), nil
	}

	return "No symbols found", nil
}

func execWorkspaceSymbol(ctx context.Context, manager *Manager, query string) (string, error) {
	servers := DetectServers(manager.workingDir)
	if len(servers) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	var allSymInfos []SymbolInformation
	var allWsSymbols []WorkspaceSymbol

	for _, server := range servers {
		session, err := manager.GetSessionByServer(ctx, server)
		if err != nil {
			continue
		}

		params := WorkspaceSymbolParams{
			Query: query,
		}

		var result json.RawMessage
		call := session.conn.Call(ctx, "workspace/symbol", params)
		if err := call.Await(ctx, &result); err != nil || result == nil || string(result) == "null" {
			continue
		}

		// Try SymbolInformation[] first (has location.uri with range)
		var symInfos []SymbolInformation
		if err := json.Unmarshal(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
			allSymInfos = append(allSymInfos, symInfos...)
			continue
		}

		// Fall back to WorkspaceSymbol[] (location range may be omitted)
		var wsSymbols []WorkspaceSymbol
		if err := json.Unmarshal(result, &wsSymbols); err == nil {
			allWsSymbols = append(allWsSymbols, wsSymbols...)
		}
	}

	if len(allSymInfos) > 0 {
		return formatSymbolInformations(allSymInfos, manager.workingDir), nil
	}

	if len(allWsSymbols) > 0 {
		return formatWorkspaceSymbols(allWsSymbols, manager.workingDir), nil
	}

	return "No symbols found", nil
}

func execWorkspaceDiagnostics(ctx context.Context, manager *Manager) (string, error) {
	servers := DetectServers(manager.workingDir)
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

		files := discoverSourceFiles(manager.workingDir, server.Languages, 50)

		for _, file := range files {
			uri, err := session.OpenDocument(ctx, file)
			if err != nil {
				continue
			}

			diags := collectDiagnostics(ctx, session, uri)
			if len(diags) == 0 {
				continue
			}

			displayPath := relPath(manager.workingDir, file)

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
func collectDiagnostics(ctx context.Context, session *Session, uri string) []Diagnostic {
	params := DocumentDiagnosticParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/diagnostic", params)
	if err := call.Await(ctx, &result); err != nil || result == nil || string(result) == "null" {
		return nil
	}

	var report FullDocumentDiagnosticReport
	if err := json.Unmarshal(result, &report); err == nil && len(report.Items) > 0 {
		return report.Items
	}

	var diagnostics []Diagnostic
	if err := json.Unmarshal(result, &diagnostics); err == nil {
		return diagnostics
	}

	return nil
}

// execCallHierarchy handles both incomingCalls and outgoingCalls.
func execCallHierarchy(ctx context.Context, session *Session, uri string, line, column int, workingDir string, incoming bool) (string, error) {
	prepareParams := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var prepareResult json.RawMessage
	call := session.conn.Call(ctx, "textDocument/prepareCallHierarchy", prepareParams)
	if err := call.Await(ctx, &prepareResult); err != nil {
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

func execIncomingCalls(ctx context.Context, session *Session, item CallHierarchyItem, workingDir string) (string, error) {
	params := CallHierarchyIncomingCallsParams{Item: item}

	var result json.RawMessage
	call := session.conn.Call(ctx, "callHierarchy/incomingCalls", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", err
	}

	var calls []CallHierarchyIncomingCall
	if err := unmarshalResult(result, &calls); err != nil {
		return "", err
	}

	if len(calls) == 0 {
		return "No incoming calls found", nil
	}

	return formatIncomingCalls(calls, workingDir), nil
}

func execOutgoingCalls(ctx context.Context, session *Session, item CallHierarchyItem, workingDir string) (string, error) {
	params := CallHierarchyOutgoingCallsParams{Item: item}

	var result json.RawMessage
	call := session.conn.Call(ctx, "callHierarchy/outgoingCalls", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", err
	}

	var calls []CallHierarchyOutgoingCall
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
