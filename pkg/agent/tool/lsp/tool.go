package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func NewTools(manager *lsp.Manager) []tool.Tool {
	return []tool.Tool{lspTool(manager)}
}

func lspTool(manager *lsp.Manager) tool.Tool {
	return tool.Tool{
		Name: "lsp",
		Description: strings.Join([]string{
			"Language-server intelligence: precise, live symbol info (exact binding, types, diagnostics). Discover files/symbols with grep/glob first, then use this for accuracy. For whole-repo structure or multi-hop call/dependency traversal (or when no server is installed), use `code_graph`.",
			"Position ops (goToDefinition / findReferences / goToImplementation / hover / incomingCalls / outgoingCalls) target `file_path` plus either `line`+`column` (1-based, as in read/grep output) or `symbol` (a name defined in the file; add `line` to pick a specific occurrence). Results include source lines; definitions include a snippet.",
			"Other operations:",
			"- documentSymbol `file_path`: symbols in one file.",
			"- workspaceSymbol `query`: symbols across the repo.",
			"- diagnostics: errors/warnings for `file_path`, or a bounded workspace scan if omitted (slower; output states how many files were checked).",
			"Errors if no language server is configured for the file type.",
		}, "\n"),
		Effect: tool.StaticEffect(tool.EffectReadOnly),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type": "string",
					"enum": []string{
						"diagnostics",
						"goToDefinition",
						"findReferences",
						"hover",
						"documentSymbol",
						"workspaceSymbol",
						"goToImplementation",
						"incomingCalls",
						"outgoingCalls",
					},
					"description": "Which operation to run.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Target file. Required for position ops and documentSymbol; optional for diagnostics.",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "1-based line (as in read/grep). Pair with `column`, or with `symbol` to pick an occurrence on that line.",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "1-based column within the line. Requires `line`.",
				},
				"symbol": map[string]any{
					"type":        "string",
					"description": "Position ops: target a symbol by name instead of line/column, e.g. `Close` or `Manager.Close`. Resolved against the file's definitions, falling back to the first textual occurrence.",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "workspaceSymbol: symbol name filter (empty lists broadly).",
				},
			},
			"required":             []string{"operation"},
			"additionalProperties": false,
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			operation, _ := args["operation"].(string)

			runPosition := func(fn func(session *lsp.Session, uri string, line, column int) (string, error)) (string, error) {
				path, err := requiredFileArg(manager.WorkingDir(), args, "file_path")
				if err != nil {
					return "", err
				}

				pos, lookup, err := resolvePosition(path, args)
				if err != nil {
					return "", err
				}

				session, uri, err := openFile(ctx, manager, path)
				if err != nil {
					return "", err
				}

				if lookup != "" {
					var ok bool
					pos, ok = session.SymbolPosition(ctx, uri, lookup)
					if !ok {
						pos, ok = lsp.PositionOfSymbol(path, symbolLeaf(lookup))
					}
					if !ok {
						return "", fmt.Errorf("symbol %q not found in %s", lookup, path)
					}
				}

				return fn(session, uri, pos.Line, pos.Character)
			}

			switch operation {
			case "diagnostics":
				path, _ := args["file_path"].(string)
				if strings.TrimSpace(path) == "" {
					return manager.WorkspaceDiagnostics(ctx)
				}

				path, err := resolveExistingFile(manager.WorkingDir(), path)
				if err != nil {
					return "", err
				}

				session, uri, err := openFile(ctx, manager, path)
				if err != nil {
					return "", err
				}

				return session.Diagnostics(ctx, uri, path)
			case "workspaceSymbol":
				query, _ := args["query"].(string)
				return manager.WorkspaceSymbols(ctx, query)
			case "documentSymbol":
				path, err := requiredFileArg(manager.WorkingDir(), args, "file_path")
				if err != nil {
					return "", err
				}
				session, uri, err := openFile(ctx, manager, path)
				if err != nil {
					return "", err
				}
				return session.DocumentSymbols(ctx, uri, path)
			case "goToDefinition":
				return runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					return s.Definition(ctx, uri, line, column)
				})
			case "findReferences":
				return runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					return s.References(ctx, uri, line, column)
				})
			case "hover":
				return runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					return s.Hover(ctx, uri, line, column)
				})
			case "goToImplementation":
				return runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					return s.Implementation(ctx, uri, line, column)
				})
			case "incomingCalls", "outgoingCalls":
				return runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					return s.CallHierarchy(ctx, uri, line, column, operation == "incomingCalls")
				})
			default:
				return "", fmt.Errorf("operation must be one of: diagnostics, goToDefinition, findReferences, hover, documentSymbol, workspaceSymbol, goToImplementation, incomingCalls, outgoingCalls")
			}
		},
	}
}

func resolvePosition(path string, args map[string]any) (lsp.Position, string, error) {
	line, hasLine, err := optionalPositiveInt(args, "line")
	if err != nil {
		return lsp.Position{}, "", err
	}

	column, hasColumn, err := optionalPositiveInt(args, "column")
	if err != nil {
		return lsp.Position{}, "", err
	}

	symbol, _ := args["symbol"].(string)
	symbol = strings.TrimSpace(symbol)

	switch {
	case hasLine && hasColumn:
		pos, err := lsp.PositionFromDisplay(path, line, column)
		return pos, "", err
	case symbol != "" && hasLine:
		pos, err := lsp.PositionOfSymbolOnLine(path, line, symbol)
		return pos, "", err
	case symbol != "":
		return lsp.Position{}, symbol, nil
	case hasLine:
		return lsp.Position{}, "", fmt.Errorf("column must be a positive 1-based integer (or pass symbol instead)")
	case hasColumn:
		return lsp.Position{}, "", fmt.Errorf("line must be a positive 1-based integer (or pass symbol instead)")
	default:
		return lsp.Position{}, "", fmt.Errorf("position required: pass line and column, or symbol (optionally with line)")
	}
}

func optionalPositiveInt(args map[string]any, key string) (int, bool, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return 0, false, nil
	}

	value, ok := tool.IntValue(raw)
	if !ok || value <= 0 {
		return 0, true, fmt.Errorf("%s must be a positive 1-based integer", key)
	}

	return value, true, nil
}

func symbolLeaf(symbol string) string {
	if idx := strings.LastIndex(symbol, "."); idx >= 0 {
		return symbol[idx+1:]
	}
	return symbol
}

func requiredFileArg(workingDir string, args map[string]any, key string) (string, error) {
	path, _ := args[key].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", key)
	}

	return resolveExistingFile(workingDir, path)
}

func resolveExistingFile(workingDir, path string) (string, error) {
	path = expandHome(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	path = filepath.Clean(path)
	workingDir = filepath.Clean(workingDir)

	if !pathInsideWorkspace(path, workingDir) {
		return "", fmt.Errorf("path %q is outside workspace %q", path, workingDir)
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", path)
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory: %s", path)
	}

	return path, nil
}

func pathInsideWorkspace(path, workingDir string) bool {
	cp, cw := path, workingDir
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		cp = strings.ToLower(cp)
		cw = strings.ToLower(cw)
	}
	if cp == cw {
		return true
	}
	return strings.HasPrefix(cp, cw+string(filepath.Separator))
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
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
