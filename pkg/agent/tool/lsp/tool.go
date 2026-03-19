package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

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

			if !slices.Contains(validOperations, operation) {
				return "", fmt.Errorf("invalid operation: %s", operation)
			}

			// Operations that don't need a file
			switch operation {
			case "workspaceDiagnostics":
				return manager.WorkspaceDiagnostics(ctx)
			case "workspaceSymbol":
				return manager.WorkspaceSymbols(ctx, query)
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
				return session.Diagnostics(ctx, uri, file)
			case "definition":
				return session.Definition(ctx, uri, line, column)
			case "references":
				return session.References(ctx, uri, line, column)
			case "implementation":
				return session.Implementation(ctx, uri, line, column)
			case "hover":
				return session.Hover(ctx, uri, line, column)
			case "documentSymbol":
				return session.DocumentSymbols(ctx, uri, file)
			case "incomingCalls":
				return session.CallHierarchy(ctx, uri, line, column, true)
			case "outgoingCalls":
				return session.CallHierarchy(ctx, uri, line, column, false)
			default:
				return "", fmt.Errorf("unknown operation: %s", operation)
			}
		},
	}
}

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
