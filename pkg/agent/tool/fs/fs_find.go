package fs

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const DefaultFindLimit = 1000

func FindTool() tool.Tool {
	return tool.Tool{
		Name: "find",

		Description: strings.Join([]string{
			fmt.Sprintf("Find files by glob pattern. Returns paths sorted by modification time (newest first). Respects .gitignore. Default limit: %d.", DefaultFindLimit),
			"",
			"Usage:",
			"- NEVER run find or ls -R via the shell — use this tool instead.",
			"- Supports glob patterns: \"**/*.go\", \"src/**/*.ts\", \"*.{js,jsx}\".",
			"- Results are sorted newest-first, so recently changed files appear at the top.",
			"- Use this to discover codebase structure before using grep or read.",
			"- For searching text inside files, use `grep` instead.",
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

		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			startTime := time.Now()

			pattern, ok := args["pattern"].(string)

			if !ok || pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}

			searchDir := "."

			if p, ok := args["path"].(string); ok && p != "" {
				searchDir = p
			}

			workingDir := env.RootDir()

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

			type fileResult struct {
				path    string
				modTime time.Time
			}
			var results []fileResult
			resultLimitReached := false

			err = walkWorkspace(ctx, fsys, searchDirFS, func(path, relPath string) error {
				// Collect more than limit to allow sorting, but cap at a reasonable number
				if len(results) >= limit*2 {
					resultLimitReached = true

					return filepath.SkipAll
				}

				matched, err := doublestar.Match(pattern, relPath)

				if err != nil {
					return nil
				}

				if matched {
					var modTime time.Time
					if fi, err := fsys.Open(path); err == nil {
						if stat, err := fi.Stat(); err == nil {
							modTime = stat.ModTime()
						}
						fi.Close()
					}
					results = append(results, fileResult{path: filepath.FromSlash(relPath), modTime: modTime})
				}

				return nil
			})

			if err != nil && err != filepath.SkipAll {
				return "", fmt.Errorf("failed to search directory: %w", err)
			}

			if len(results) == 0 {
				return "No files found matching pattern", nil
			}

			// Sort by modification time (newest first)
			sort.Slice(results, func(i, j int) bool {
				return results[i].modTime.After(results[j].modTime)
			})

			// Apply limit
			if len(results) > limit {
				results = results[:limit]
				resultLimitReached = true
			}

			paths := make([]string, len(results))
			for i, r := range results {
				paths[i] = r.path
			}

			rawOutput := strings.Join(paths, "\n")
			truncatedOutput, _, bytesTruncated := truncateHead(rawOutput)

			duration := time.Since(startTime)

			var notices []string

			notices = append(notices, fmt.Sprintf("%d files found in %dms", len(results), duration.Milliseconds()))

			if resultLimitReached {
				notices = append(notices, fmt.Sprintf("%d results limit reached — refine the pattern for more specific results", limit))
			}

			if bytesTruncated {
				notices = append(notices, fmt.Sprintf("%dKB limit reached", DefaultMaxBytes/1024))
			}

			truncatedOutput += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))

			return truncatedOutput, nil
		},
	}
}
