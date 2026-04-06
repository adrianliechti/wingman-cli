package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
	"github.com/adrianliechti/wingman-agent/pkg/agent/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// NewTools creates the LSP tools for coding agents.
func NewTools(manager *lsp.Manager) []tool.Tool {
	return []tool.Tool{
		diagnosticsTool(manager),
		definitionTool(manager),
		referencesTool(manager),
		implementationTool(manager),
		hoverTool(manager),
		symbolsTool(manager),
		hierarchyTool(manager),
	}
}

func diagnosticsTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name:            "get_lsp_diagnostics",
		Description:     "Get diagnostics (errors, warnings) for a file or the entire workspace.",
		ConcurrencySafe: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute file path. Omit for all diagnostics.",
				},
			},
		},
		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			path, _ := args["path"].(string)

			if path == "" {
				return manager.WorkspaceDiagnostics(ctx)
			}

			path = absPath(manager.WorkingDir(), path)

			if _, err := os.Stat(path); os.IsNotExist(err) {
				return "", fmt.Errorf("file not found: %s", path)
			}

			session, err := manager.GetSession(ctx, path)
			if err != nil {
				return "", err
			}

			uri, err := session.OpenDocument(ctx, path)
			if err != nil {
				return "", err
			}

			return session.Diagnostics(ctx, uri, path)
		},
	}
}

func definitionTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name:            "find_lsp_definition",
		Description:     "Find the definition of a symbol at a given position.",
		ConcurrencySafe: true,
		Parameters:      positionParams(),
		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			path, line, column, err := parsePositionArgs(manager.WorkingDir(), args)
			if err != nil {
				return "", err
			}

			session, uri, err := openFile(ctx, manager, path)
			if err != nil {
				return "", err
			}

			return session.Definition(ctx, uri, line, column)
		},
	}
}

func referencesTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name:            "find_lsp_references",
		Description:     "Find all references to a symbol at a given position across the workspace.",
		ConcurrencySafe: true,
		Parameters:      positionParams(),
		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			path, line, column, err := parsePositionArgs(manager.WorkingDir(), args)
			if err != nil {
				return "", err
			}

			session, uri, err := openFile(ctx, manager, path)
			if err != nil {
				return "", err
			}

			return session.References(ctx, uri, line, column)
		},
	}
}

func implementationTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name:            "find_lsp_implementation",
		Description:     "Find implementations of an interface or abstract method at a given position.",
		ConcurrencySafe: true,
		Parameters:      positionParams(),
		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			path, line, column, err := parsePositionArgs(manager.WorkingDir(), args)
			if err != nil {
				return "", err
			}

			session, uri, err := openFile(ctx, manager, path)
			if err != nil {
				return "", err
			}

			return session.Implementation(ctx, uri, line, column)
		},
	}
}

func hoverTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name:            "get_lsp_hover",
		Description:     "Get hover information (type info, documentation) for a symbol at a given position.",
		ConcurrencySafe: true,
		Parameters:      positionParams(),
		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			path, line, column, err := parsePositionArgs(manager.WorkingDir(), args)
			if err != nil {
				return "", err
			}

			session, uri, err := openFile(ctx, manager, path)
			if err != nil {
				return "", err
			}

			return session.Hover(ctx, uri, line, column)
		},
	}
}

func symbolsTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name:            "find_lsp_symbols",
		Description:     "Get symbols. With a path: returns the symbol outline (functions, classes, variables) of that file. Without a path: searches symbols across the entire workspace by query.",
		ConcurrencySafe: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to a file. If provided, returns symbols in that file.",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Search query for workspace-wide symbol search. Used when path is omitted.",
				},
			},
		},
		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			query, _ := args["query"].(string)

			if path == "" {
				return manager.WorkspaceSymbols(ctx, query)
			}

			path = absPath(manager.WorkingDir(), path)

			if _, err := os.Stat(path); os.IsNotExist(err) {
				return "", fmt.Errorf("file not found: %s", path)
			}

			session, err := manager.GetSession(ctx, path)
			if err != nil {
				return "", err
			}

			uri, err := session.OpenDocument(ctx, path)
			if err != nil {
				return "", err
			}

			return session.DocumentSymbols(ctx, uri, path)
		},
	}
}

func hierarchyTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name:            "find_lsp_hierarchy",
		Description:     "Get incoming and outgoing calls for a function/method at a given position.",
		ConcurrencySafe: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "Line number (0-based)",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "Column number (0-based)",
				},
				"direction": map[string]any{
					"type":        "string",
					"enum":        []string{"incoming", "outgoing"},
					"description": "Direction of the call hierarchy",
				},
			},
			"required": []string{"path", "line", "column", "direction"},
		},
		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			path, line, column, err := parsePositionArgs(manager.WorkingDir(), args)
			if err != nil {
				return "", err
			}

			direction, _ := args["direction"].(string)
			if direction != "incoming" && direction != "outgoing" {
				return "", fmt.Errorf("direction must be 'incoming' or 'outgoing'")
			}

			session, uri, err := openFile(ctx, manager, path)
			if err != nil {
				return "", err
			}

			return session.CallHierarchy(ctx, uri, line, column, direction == "incoming")
		},
	}
}

// --- helpers ---

func positionParams() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file",
			},
			"line": map[string]any{
				"type":        "integer",
				"description": "Line number (0-based)",
			},
			"column": map[string]any{
				"type":        "integer",
				"description": "Column number (0-based)",
			},
		},
		"required": []string{"path", "line", "column"},
	}
}

func parsePositionArgs(workingDir string, args map[string]any) (string, int, int, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", 0, 0, fmt.Errorf("path is required")
	}

	path = absPath(workingDir, path)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", 0, 0, fmt.Errorf("file not found: %s", path)
	}

	line := intArg(args, "line")
	column := intArg(args, "column")

	return path, line, column, nil
}

func openFile(ctx context.Context, manager *lsp.Manager, path string) (*lsp.Session, string, error) {
	session, err := manager.GetSession(ctx, path)
	if err != nil {
		return nil, "", err
	}

	uri, err := session.OpenDocument(ctx, path)
	if err != nil {
		return nil, "", err
	}

	return session, uri, nil
}

func absPath(workingDir, path string) string {
	if !filepath.IsAbs(path) {
		return filepath.Join(workingDir, path)
	}
	return path
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
