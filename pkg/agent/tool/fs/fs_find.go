package fs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const DefaultFindLimit = 1000

func FindTool() tool.Tool {
	return tool.Tool{
		Name: "find",

		Description: strings.Join([]string{
			fmt.Sprintf("Fast file pattern matching. Returns matching paths relative to search directory. Respects .gitignore. Truncated to %d results or %dKB.", DefaultFindLimit, DefaultMaxBytes/1024),
			"",
			"Usage:",
			"- Use this tool to find files by name or extension. NEVER run find or ls -R via the shell.",
			"- Supports glob patterns like \"**/*.go\", \"src/**/*.ts\", \"*.{js,jsx}\".",
			"- Use this when exploring a codebase to discover structure before using grep or read.",
			"- For content search (finding text inside files), use `grep` instead.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files (e.g., \"**/*.go\", \"src/**/*.ts\")"},
				"path":    map[string]any{"type": "string", "description": "Directory to search in (defaults to working directory)"},
				"limit":   map[string]any{"type": "integer", "description": "Maximum number of results to return (default: 1000)"},
			},
			"required": []string{"pattern"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			pattern, ok := args["pattern"].(string)

			if !ok || pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}

			searchDir := "."

			if p, ok := args["path"].(string); ok && p != "" {
				searchDir = p
			}

			workingDir := env.WorkingDir()

			searchDirFS, err := ensurePathInWorkspaceFS(searchDir, workingDir, "search")

			if err != nil {
				return "", err
			}

			limit := DefaultFindLimit

			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			info, err := env.Root.Stat(searchDirFS)

			if err != nil {
				return "", pathError("stat path", searchDir, searchDirFS, workingDir, err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("path is not a directory: %s", searchDir)
			}

			fsys := env.Root.FS()
			var results []string
			resultLimitReached := false

			err = walkWorkspace(ctx, fsys, searchDirFS, func(path, relPath string) error {
				if len(results) >= limit {
					resultLimitReached = true

					return filepath.SkipAll
				}

				matched, err := doublestar.Match(pattern, relPath)

				if err != nil {
					return nil
				}

				if matched {
					results = append(results, filepath.FromSlash(relPath))
				}

				return nil
			})

			if err != nil && err != filepath.SkipAll {
				return "", fmt.Errorf("failed to search directory: %w", err)
			}

			if len(results) == 0 {
				return "No files found matching pattern", nil
			}

			rawOutput := strings.Join(results, "\n")
			truncatedOutput, _, bytesTruncated := truncateHead(rawOutput)

			var notices []string

			if resultLimitReached {
				notices = append(notices, fmt.Sprintf("%d results limit reached. Use limit=%d for more, or refine pattern", limit, limit*2))
			}

			if bytesTruncated {
				notices = append(notices, fmt.Sprintf("%dKB limit reached", DefaultMaxBytes/1024))
			}

			if len(notices) > 0 {
				truncatedOutput += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			return truncatedOutput, nil
		},
	}
}
