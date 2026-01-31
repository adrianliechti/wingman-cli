package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
}

// NewTool creates an LSP tool that connects on-demand for each invocation.
func NewTool(workingDir string) tool.Tool {
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
					"description": "Absolute or relative path to the source file",
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
			},
			"required": []string{"operation"},
		},
		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			operation, _ := args["operation"].(string)
			file, _ := args["file"].(string)
			line := intArg(args, "line")
			column := intArg(args, "column")
			query, _ := args["query"].(string)

			// Validate operation
			if !isValidOperation(operation) {
				return "", fmt.Errorf("invalid operation: %s", operation)
			}

			// File is always required (anchors which LSP server to use)
			if file == "" {
				return "", fmt.Errorf("file is required for %s operation", operation)
			}

			needsPosition := operation != "documentSymbols" && operation != "workspaceSymbols"

			if needsPosition && (line == 0 || column == 0) {
				return "", fmt.Errorf("line and column are required for %s operation", operation)
			}

			// Make path absolute if relative
			if file != "" && !filepath.IsAbs(file) {
				file = filepath.Join(workingDir, file)
			}

			// Check file exists
			if file != "" {
				if _, err := os.Stat(file); os.IsNotExist(err) {
					return "", fmt.Errorf("file not found: %s", file)
				}
			}

			// Connect to appropriate LSP server
			session, err := Connect(ctx, workingDir, file)
			if err != nil {
				return "", err
			}
			defer session.Close()

			// Open document
			uri, err := session.OpenDocument(ctx, file)
			if err != nil {
				return "", err
			}

			// Execute operation
			switch operation {
			case "definition":
				return execDefinition(ctx, session, uri, line, column, workingDir)
			case "references":
				return execReferences(ctx, session, uri, line, column, workingDir)
			case "hover":
				return execHover(ctx, session, uri, line, column)
			case "implementation":
				return execImplementation(ctx, session, uri, line, column, workingDir)
			case "documentSymbols":
				return execDocumentSymbols(ctx, session, uri)
			case "workspaceSymbols":
				return execWorkspaceSymbols(ctx, session, query, workingDir)
			case "incomingCalls":
				return execIncomingCalls(ctx, session, uri, line, column, workingDir)
			case "outgoingCalls":
				return execOutgoingCalls(ctx, session, uri, line, column, workingDir)
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

// execWorkspaceSymbols searches for symbols across the workspace.
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
