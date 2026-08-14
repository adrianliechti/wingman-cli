package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/language"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func NewTools(service *language.Service) []tool.Tool {
	return []tool.Tool{lspTool(service)}
}

func lspTool(service *language.Service) tool.Tool {
	manager := service.Manager()
	return tool.Tool{
		Name: "lsp",
		Description: strings.Join([]string{
			"Language-server intelligence: precise, live symbol info (exact binding, types, diagnostics). Discover files/symbols with grep/glob first, then use this for accuracy. For whole-repo structure or multi-hop call/dependency traversal (or when no server is installed), use `code_graph`.",
			"Position ops (goToDefinition / findReferences / goToImplementation / hover / incomingCalls / outgoingCalls) target `file_path` plus either `line`+`column` (1-based, as in read/grep output) or `symbol` (a name defined in the file; add `line` to pick a specific occurrence). Results include source lines; definitions include a snippet.",
			"Other operations:",
			"- documentSymbol `file_path`: symbols in one file.",
			"- workspaceSymbol `query`: symbols across the repo.",
			"- diagnostics: errors/warnings for `file_path`, or a bounded workspace scan if omitted (slower; output states how many files were checked).",
			"The first request per language may take a few seconds while the server starts and indexes. Errors if no language server is configured for the file type.",
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
					"minimum":     1,
					"description": "1-based line (as in read/grep). Pair with `column`, or with `symbol` to pick an occurrence on that line.",
				},
				"column": map[string]any{
					"type":        "integer",
					"minimum":     1,
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
		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
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
					if pos, ok = symbolPosition(ctx, session, uri, lookup); !ok {
						return "", fmt.Errorf("symbol %q not found in %s", lookup, path)
					}
				}

				return fn(session, uri, int(pos.Line), int(pos.Character))
			}

			switch operation {
			case "diagnostics":
				path, _ := args["file_path"].(string)
				if strings.TrimSpace(path) == "" {
					if len(manager.DetectServers()) == 0 {
						return tool.Result{}, fmt.Errorf("no LSP servers detected in workspace")
					}
					return tool.Text(formatWorkspaceDiagnostics(service.Diagnostics(ctx), manager.WorkingDir())), nil
				}

				path, err := resolveExistingFile(manager.WorkingDir(), path)
				if err != nil {
					return tool.Result{}, err
				}

				session, uri, err := openFile(ctx, manager, path)
				if err != nil {
					return tool.Result{}, err
				}

				raw, known := session.WaitForDiagnostics(ctx, uri, 3*time.Second)
				if !known {
					return tool.Text("No diagnostics data: the language server did not report results for this file (it may still be analyzing or not support diagnostics). Do not treat this as a clean result."), nil
				}
				values := language.DiagnosticsFromProtocol(raw)
				if len(values) == 0 {
					return tool.Text("No diagnostics found"), nil
				}
				return tool.Text(language.FormatDiagnostics(values, path, manager.WorkingDir())), nil
			case "workspaceSymbol":
				query, _ := args["query"].(string)
				symbols, workspaceSymbols, err := service.WorkspaceSymbols(ctx, query)
				if err != nil {
					return tool.Result{}, err
				}
				if len(symbols) > 0 {
					return tool.Text(formatSymbolInformation(symbols, manager.WorkingDir())), nil
				}
				if len(workspaceSymbols) > 0 {
					return tool.Text(formatWorkspaceSymbols(workspaceSymbols, manager.WorkingDir())), nil
				}
				return tool.Text("No symbols found"), nil
			case "documentSymbol":
				path, err := requiredFileArg(manager.WorkingDir(), args, "file_path")
				if err != nil {
					return tool.Result{}, err
				}
				session, uri, err := openFile(ctx, manager, path)
				if err != nil {
					return tool.Result{}, err
				}
				result, err := session.DocumentSymbols(ctx, uri)
				documentSymbols, symbols := language.DocumentSymbolsFromProtocol(result)
				if err != nil {
					return tool.Result{}, err
				}
				if len(symbols) > 0 {
					return tool.Text(formatSymbolInformation(symbols, manager.WorkingDir())), nil
				}
				if len(documentSymbols) > 0 {
					return tool.Text(formatDocumentSymbols(documentSymbols, path, manager.WorkingDir(), 0)), nil
				}
				return tool.Text("No symbols found"), nil
			case "goToDefinition":
				content, err := runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					locations, err := s.DefinitionLocations(ctx, uri, line, column)
					if err != nil || len(locations) == 0 {
						return "No definition found", err
					}
					return formatDefinitions(locations, manager.WorkingDir()), nil
				})
				return tool.Text(content), err
			case "findReferences":
				content, err := runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					locations, err := s.ReferenceLocations(ctx, uri, line, column)
					if err != nil || len(locations) == 0 {
						return "No references found", err
					}
					return formatLocations("References", locations, manager.WorkingDir()), nil
				})
				return tool.Text(content), err
			case "hover":
				content, err := runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					hover, err := s.Hover(ctx, uri, line, column)
					content := language.HoverText(hover)
					if err == nil && content == "" {
						content = "No hover information available"
					}
					return content, err
				})
				return tool.Text(content), err
			case "goToImplementation":
				content, err := runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					locations, err := s.ImplementationLocations(ctx, uri, line, column)
					if err != nil || len(locations) == 0 {
						return "No implementations found", err
					}
					return formatLocations("Implementations", locations, manager.WorkingDir()), nil
				})
				return tool.Text(content), err
			case "incomingCalls", "outgoingCalls":
				content, err := runPosition(func(s *lsp.Session, uri string, line, column int) (string, error) {
					items, err := s.PrepareCallHierarchy(ctx, uri, line, column)
					if err != nil || len(items) == 0 {
						return "No call hierarchy item found at this position", err
					}
					var content string
					if operation == "incomingCalls" {
						calls, err := s.IncomingCalls(ctx, items[0])
						if err != nil || len(calls) == 0 {
							return "No incoming calls found", err
						}
						content = formatIncomingCalls(calls, manager.WorkingDir())
					} else {
						calls, err := s.OutgoingCalls(ctx, items[0])
						if err != nil || len(calls) == 0 {
							return "No outgoing calls found", err
						}
						content = formatOutgoingCalls(calls, manager.WorkingDir())
					}
					if len(items) > 1 {
						content += fmt.Sprintf("(%d call hierarchy items at this position; showing calls for %s)\n", len(items), items[0].Name)
					}
					return content, nil
				})
				return tool.Text(content), err
			default:
				return tool.Result{}, fmt.Errorf("operation must be one of: diagnostics, goToDefinition, findReferences, hover, documentSymbol, workspaceSymbol, goToImplementation, incomingCalls, outgoingCalls")
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
		pos, err := positionFromDisplay(path, line, column)
		return pos, "", err
	case symbol != "" && hasLine:
		pos, err := positionOfSymbolOnLine(path, line, symbol)
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
