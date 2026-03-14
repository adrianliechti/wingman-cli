package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/prompt"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

var validOperations = []string{
	"definition",
	"references",
	"hover",
	"implementation",
	"documentSymbols",
	"workspaceSymbols",
	"incomingCalls",
	"outgoingCalls",
	"diagnostics",
	"workspaceDiagnostics",
	"rename",
	"codeActions",
}

// NewTool creates an LSP tool that uses a Manager for session caching.
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
					"description": "Absolute or relative path to the source file (not required for workspaceDiagnostics and workspaceSymbols)",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "Line number (1-based, as shown in editors)",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "Column/character offset (1-based, as shown in editors)",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Search query for workspaceSymbols operation",
				},
				"newName": map[string]any{
					"type":        "string",
					"description": "New name for the symbol (required for rename operation)",
				},
			},
			"required": []string{"operation"},
		},
		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			operation, _ := args["operation"].(string)
			file, _ := args["file"].(string)
			line := intArg(args, "line")
			column := intArg(args, "column")
			query, _ := args["query"].(string)
			newName, _ := args["newName"].(string)

			// Validate operation
			if !isValidOperation(operation) {
				return "", fmt.Errorf("invalid operation: %s", operation)
			}

			// Workspace-level operations that don't require a file
			isWorkspaceOp := operation == "workspaceDiagnostics" || operation == "workspaceSymbols"

			if !isWorkspaceOp && file == "" {
				return "", fmt.Errorf("file is required for %s operation", operation)
			}

			// Determine if operation needs position (line/column)
			needsPosition := operation != "documentSymbols" && operation != "workspaceSymbols" && operation != "diagnostics" && operation != "codeActions" && operation != "workspaceDiagnostics"

			if needsPosition && (line == 0 || column == 0) {
				return "", fmt.Errorf("line and column are required for %s operation", operation)
			}

			// Rename requires newName
			if operation == "rename" && newName == "" {
				return "", fmt.Errorf("newName is required for rename operation")
			}

			// Handle workspace-level operations (no file required)
			if operation == "workspaceDiagnostics" {
				return execWorkspaceDiagnostics(ctx, manager)
			}

			if operation == "workspaceSymbols" && file == "" {
				return execWorkspaceSymbolsAll(ctx, manager, query)
			}

			// Make path absolute if relative
			if file != "" && !filepath.IsAbs(file) {
				file = filepath.Join(manager.workingDir, file)
			}

			// Check file exists
			if _, err := os.Stat(file); os.IsNotExist(err) {
				return "", fmt.Errorf("file not found: %s", file)
			}

			// Get or create a cached LSP session
			session, err := manager.GetSession(ctx, file)
			if err != nil {
				return "", err
			}

			// Open/sync document for operations that need it
			var uri string
			if operation != "workspaceSymbols" {
				uri, err = session.OpenDocument(ctx, file)
				if err != nil {
					return "", err
				}
			}

			// Execute operation
			switch operation {
			case "definition":
				return execDefinition(ctx, session, uri, line, column, manager.workingDir)
			case "references":
				return execReferences(ctx, session, uri, line, column, manager.workingDir)
			case "hover":
				return execHover(ctx, session, uri, line, column)
			case "implementation":
				return execImplementation(ctx, session, uri, line, column, manager.workingDir)
			case "documentSymbols":
				return execDocumentSymbols(ctx, session, uri)
			case "workspaceSymbols":
				return execWorkspaceSymbols(ctx, session, query, manager.workingDir)
			case "incomingCalls":
				return execIncomingCalls(ctx, session, uri, line, column, manager.workingDir)
			case "outgoingCalls":
				return execOutgoingCalls(ctx, session, uri, line, column, manager.workingDir)
			case "diagnostics":
				return execDiagnostics(ctx, session, uri, file, manager.workingDir)
			case "rename":
				return execRename(ctx, session, uri, line, column, newName, manager.workingDir)
			case "codeActions":
				return execCodeActions(ctx, session, uri, file, manager.workingDir)
			default:
				return "", fmt.Errorf("unknown operation: %s", operation)
			}
		},
	}
}

func isValidOperation(op string) bool {
	for _, v := range validOperations {
		if v == op {
			return true
		}
	}
	return false
}

// execDefinition finds the definition of a symbol.
func execDefinition(ctx context.Context, session *Session, uri string, line, column int, workingDir string) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/definition", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", err
	}

	locations, err := parseLocationResponse(result)
	if err != nil {
		return "", err
	}

	if len(locations) == 0 {
		return "No definition found", nil
	}

	return formatLocations("Definition", locations, workingDir), nil
}

// execReferences finds all references to a symbol.
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

// execHover gets hover information for a symbol.
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
		var rawContent any
		if err := json.Unmarshal(result, &rawContent); err == nil {
			return fmt.Sprintf("%v", rawContent), nil
		}
		return "", err
	}

	return hover.Contents.Value, nil
}

// execImplementation finds implementations of an interface or abstract method.
func execImplementation(ctx context.Context, session *Session, uri string, line, column int, workingDir string) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/implementation", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", err
	}

	locations, err := parseLocationResponse(result)
	if err != nil {
		return "", err
	}

	if len(locations) == 0 {
		return "No implementations found", nil
	}

	return formatLocations("Implementations", locations, workingDir), nil
}

// execDocumentSymbols gets all symbols in a document.
func execDocumentSymbols(ctx context.Context, session *Session, uri string) (string, error) {
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/documentSymbol", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", err
	}

	// Try DocumentSymbol first (hierarchical)
	var docSymbols []DocumentSymbol
	if err := json.Unmarshal(result, &docSymbols); err == nil && len(docSymbols) > 0 {
		return formatDocumentSymbols(docSymbols, 0), nil
	}

	// Try SymbolInformation (flat)
	var symInfos []SymbolInformation
	if err := json.Unmarshal(result, &symInfos); err == nil {
		return formatSymbolInformation(symInfos), nil
	}

	return "No symbols found", nil
}

// execWorkspaceSymbols searches for symbols across the workspace using a specific session.
func execWorkspaceSymbols(ctx context.Context, session *Session, query string, workingDir string) (string, error) {
	params := WorkspaceSymbolParams{
		Query: query,
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "workspace/symbol", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", err
	}

	var symbols []SymbolInformation
	if err := json.Unmarshal(result, &symbols); err != nil {
		return "", err
	}

	if len(symbols) == 0 {
		return "No symbols found", nil
	}

	return formatWorkspaceSymbols(symbols, workingDir), nil
}

// execWorkspaceSymbolsAll searches for symbols across all detected LSP servers.
func execWorkspaceSymbolsAll(ctx context.Context, manager *Manager, query string) (string, error) {
	servers := DetectServers(manager.workingDir)
	if len(servers) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	var allSymbols []SymbolInformation

	for _, server := range servers {
		session, err := manager.GetSessionByServer(ctx, server)
		if err != nil {
			continue
		}

		params := WorkspaceSymbolParams{Query: query}

		var result json.RawMessage
		call := session.conn.Call(ctx, "workspace/symbol", params)
		if err := call.Await(ctx, &result); err != nil {
			continue
		}

		var symbols []SymbolInformation
		if err := json.Unmarshal(result, &symbols); err != nil {
			continue
		}

		allSymbols = append(allSymbols, symbols...)
	}

	if len(allSymbols) == 0 {
		return "No symbols found", nil
	}

	return formatWorkspaceSymbols(allSymbols, manager.workingDir), nil
}

// execIncomingCalls finds all callers of a function.
func execIncomingCalls(ctx context.Context, session *Session, uri string, line, column int, workingDir string) (string, error) {
	// First, prepare call hierarchy
	prepareParams := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var prepareResult json.RawMessage
	prepareCall := session.conn.Call(ctx, "textDocument/prepareCallHierarchy", prepareParams)
	if err := prepareCall.Await(ctx, &prepareResult); err != nil {
		return "", fmt.Errorf("prepareCallHierarchy: %w", err)
	}

	var items []CallHierarchyItem
	if err := json.Unmarshal(prepareResult, &items); err != nil {
		return "", err
	}

	if len(items) == 0 {
		return "No call hierarchy item found at this position", nil
	}

	// Get incoming calls for the first item
	incomingParams := CallHierarchyIncomingCallsParams{
		Item: items[0],
	}

	var incomingResult json.RawMessage
	incomingCall := session.conn.Call(ctx, "callHierarchy/incomingCalls", incomingParams)
	if err := incomingCall.Await(ctx, &incomingResult); err != nil {
		return "", fmt.Errorf("incomingCalls: %w", err)
	}

	var calls []CallHierarchyIncomingCall
	if err := json.Unmarshal(incomingResult, &calls); err != nil {
		return "", err
	}

	if len(calls) == 0 {
		return "No incoming calls found", nil
	}

	return formatIncomingCalls(calls, workingDir), nil
}

// execOutgoingCalls finds all functions called by a function.
func execOutgoingCalls(ctx context.Context, session *Session, uri string, line, column int, workingDir string) (string, error) {
	// First, prepare call hierarchy
	prepareParams := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var prepareResult json.RawMessage
	prepareCall := session.conn.Call(ctx, "textDocument/prepareCallHierarchy", prepareParams)
	if err := prepareCall.Await(ctx, &prepareResult); err != nil {
		return "", fmt.Errorf("prepareCallHierarchy: %w", err)
	}

	var items []CallHierarchyItem
	if err := json.Unmarshal(prepareResult, &items); err != nil {
		return "", err
	}

	if len(items) == 0 {
		return "No call hierarchy item found at this position", nil
	}

	// Get outgoing calls for the first item
	outgoingParams := CallHierarchyOutgoingCallsParams{
		Item: items[0],
	}

	var outgoingResult json.RawMessage
	outgoingCall := session.conn.Call(ctx, "callHierarchy/outgoingCalls", outgoingParams)
	if err := outgoingCall.Await(ctx, &outgoingResult); err != nil {
		return "", fmt.Errorf("outgoingCalls: %w", err)
	}

	var calls []CallHierarchyOutgoingCall
	if err := json.Unmarshal(outgoingResult, &calls); err != nil {
		return "", err
	}

	if len(calls) == 0 {
		return "No outgoing calls found", nil
	}

	return formatOutgoingCalls(calls, workingDir), nil
}

// execDiagnostics gets diagnostics (errors, warnings) for a single file.
func execDiagnostics(ctx context.Context, session *Session, uri string, filePath string, workingDir string) (string, error) {
	// Try pull diagnostics first (LSP 3.17+)
	params := DocumentDiagnosticParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/diagnostic", params)
	if err := call.Await(ctx, &result); err == nil && result != nil && string(result) != "null" {
		// Parse full document diagnostic report
		var report FullDocumentDiagnosticReport
		if err := json.Unmarshal(result, &report); err == nil {
			if len(report.Items) == 0 {
				return "No diagnostics found", nil
			}
			return formatDiagnostics(report.Items, filePath, workingDir), nil
		}

		// Try parsing as direct array of diagnostics
		var diagnostics []Diagnostic
		if err := json.Unmarshal(result, &diagnostics); err == nil {
			if len(diagnostics) == 0 {
				return "No diagnostics found", nil
			}
			return formatDiagnostics(diagnostics, filePath, workingDir), nil
		}
	}

	// Pull diagnostics not supported or failed - return message
	return "No diagnostics available (server may not support pull diagnostics)", nil
}

// execWorkspaceDiagnostics gets diagnostics across the entire workspace by
// discovering source files and requesting per-file diagnostics.
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

			params := DocumentDiagnosticParams{
				TextDocument: TextDocumentIdentifier{URI: uri},
			}

			var result json.RawMessage
			call := session.conn.Call(ctx, "textDocument/diagnostic", params)
			if err := call.Await(ctx, &result); err != nil || result == nil || string(result) == "null" {
				continue
			}

			displayPath := file
			if rel, err := filepath.Rel(manager.workingDir, file); err == nil && !strings.HasPrefix(rel, "..") {
				displayPath = rel
			}

			// Try FullDocumentDiagnosticReport
			var report FullDocumentDiagnosticReport
			if err := json.Unmarshal(result, &report); err == nil && len(report.Items) > 0 {
				for _, diag := range report.Items {
					totalDiags++
					fmt.Fprintf(&sb, "  %s:%d:%d %s: %s\n", displayPath, diag.Range.Start.Line+1, diag.Range.Start.Character+1, diagnosticSeverityName(diag.Severity), diag.Message)
				}
				continue
			}

			// Try direct array
			var diagnostics []Diagnostic
			if err := json.Unmarshal(result, &diagnostics); err == nil {
				for _, diag := range diagnostics {
					totalDiags++
					fmt.Fprintf(&sb, "  %s:%d:%d %s: %s\n", displayPath, diag.Range.Start.Line+1, diag.Range.Start.Character+1, diagnosticSeverityName(diag.Severity), diag.Message)
				}
			}
		}
	}

	if totalDiags == 0 {
		return "No workspace diagnostics found", nil
	}

	return fmt.Sprintf("Workspace Diagnostics (%d found):\n%s", totalDiags, sb.String()), nil
}

// discoverSourceFiles finds source files in the workspace matching the given extensions.
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

		// Skip hidden directories and common non-source directories
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

// execRename renames a symbol across the workspace.
func execRename(ctx context.Context, session *Session, uri string, line, column int, newName string, workingDir string) (string, error) {
	params := RenameParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
		NewName:      newName,
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/rename", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	if result == nil || string(result) == "null" {
		return "Rename not available at this position", nil
	}

	var edit WorkspaceEdit
	if err := json.Unmarshal(result, &edit); err != nil {
		return "", fmt.Errorf("parse workspace edit: %w", err)
	}

	if len(edit.Changes) == 0 {
		return "No changes needed for rename", nil
	}

	return formatWorkspaceEdit(&edit, workingDir), nil
}

// execCodeActions gets available code actions for a file.
func execCodeActions(ctx context.Context, session *Session, uri string, filePath string, workingDir string) (string, error) {
	// Read file to get full range
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	endLine := len(lines) - 1
	endChar := 0
	if endLine >= 0 && len(lines[endLine]) > 0 {
		endChar = len(lines[endLine])
	}

	params := CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: endLine, Character: endChar},
		},
		Context: CodeActionContext{
			Diagnostics: []Diagnostic{},
		},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/codeAction", params)
	if err := call.Await(ctx, &result); err != nil {
		return "", fmt.Errorf("codeAction: %w", err)
	}

	if result == nil || string(result) == "null" {
		return "No code actions available", nil
	}

	var actions []CodeAction
	if err := json.Unmarshal(result, &actions); err != nil {
		return "", fmt.Errorf("parse code actions: %w", err)
	}

	if len(actions) == 0 {
		return "No code actions available", nil
	}

	return formatCodeActions(actions, workingDir), nil
}
