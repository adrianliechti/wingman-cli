package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/tool"
)

var validOperations = []string{
	"diagnostics",
	"workspaceDiagnostics",
	"definition",
	"references",
	"hover",
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
					"description": "Path to the source file (not required for workspaceDiagnostics)",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "Line number (1-based)",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "Column offset (1-based)",
				},
			},
			"required": []string{"operation"},
		},
		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			operation, _ := args["operation"].(string)
			file, _ := args["file"].(string)
			line := intArg(args, "line")
			column := intArg(args, "column")

			if !isValidOperation(operation) {
				return "", fmt.Errorf("invalid operation: %s", operation)
			}

			// workspaceDiagnostics needs no file
			if operation == "workspaceDiagnostics" {
				return execWorkspaceDiagnostics(ctx, manager)
			}

			if file == "" {
				return "", fmt.Errorf("file is required for %s operation", operation)
			}

			// definition, references, hover need line+column
			needsPosition := operation != "diagnostics"
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
				return execDefinition(ctx, session, uri, line, column, manager.workingDir)
			case "references":
				return execReferences(ctx, session, uri, line, column, manager.workingDir)
			case "hover":
				return execHover(ctx, session, uri, line, column)
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

func execDiagnostics(ctx context.Context, session *Session, uri string, filePath string, workingDir string) (string, error) {
	params := DocumentDiagnosticParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	call := session.conn.Call(ctx, "textDocument/diagnostic", params)
	if err := call.Await(ctx, &result); err == nil && result != nil && string(result) != "null" {
		var report FullDocumentDiagnosticReport
		if err := json.Unmarshal(result, &report); err == nil && len(report.Items) > 0 {
			return formatDiagnostics(report.Items, filePath, workingDir), nil
		}

		var diagnostics []Diagnostic
		if err := json.Unmarshal(result, &diagnostics); err == nil && len(diagnostics) > 0 {
			return formatDiagnostics(diagnostics, filePath, workingDir), nil
		}
	}

	return "No diagnostics found", nil
}

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
		var rawContent any
		if err := json.Unmarshal(result, &rawContent); err == nil {
			return fmt.Sprintf("%v", rawContent), nil
		}
		return "", err
	}

	return hover.Contents.Value, nil
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

			var report FullDocumentDiagnosticReport
			if err := json.Unmarshal(result, &report); err == nil && len(report.Items) > 0 {
				for _, diag := range report.Items {
					totalDiags++
					fmt.Fprintf(&sb, "  %s:%d:%d %s: %s\n", displayPath, diag.Range.Start.Line+1, diag.Range.Start.Character+1, diagnosticSeverityName(diag.Severity), diag.Message)
				}
				continue
			}

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
