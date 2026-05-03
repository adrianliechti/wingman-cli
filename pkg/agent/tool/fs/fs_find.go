package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const DefaultFindLimit = 100

func FindTool(root *os.Root) tool.Tool {
	return tool.Tool{
		Name:   "find",
		Effect: tool.StaticEffect(tool.EffectReadOnly),

		Description: strings.Join([]string{
			fmt.Sprintf("Find files by glob pattern. Returns paths sorted by modification time (newest first). Respects .gitignore. Default limit: %d.", DefaultFindLimit),
			"",
			"Usage:",
			"- Prefer this tool over shell `find` / `ls -R` for filename pattern discovery.",
			"- Use `grep` (which lists matching files) when you're looking for content rather than filenames — it often replaces a separate `find` call.",
			"- Supports glob patterns: \"**/*.go\", \"src/**/*.ts\", \"*.{js,jsx}\".",
			"- Results are sorted newest-first, so recently changed files appear at the top.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files (e.g., \"**/*.go\", \"src/**/*.ts\")"},
				"path":    map[string]any{"type": "string", "description": "Directory to search in (defaults to working directory)"},
				"limit":   map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum number of results to return (default: %d)", DefaultFindLimit)},
			},
			"required": []string{"pattern"},
		},

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			startTime := time.Now()

			pattern, ok := args["pattern"].(string)

			if !ok || pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}

			searchDir := "."

			if p, ok := args["path"].(string); ok && p != "" {
				searchDir = p
			}

			workingDir := root.Name()

			searchDirFS, err := ensurePathInWorkspaceFS(searchDir, workingDir, "search")

			if err != nil {
				return "", err
			}

			limit := DefaultFindLimit

			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			info, err := root.Stat(searchDirFS)

			if err != nil {
				return "", pathError("stat path", searchDir, searchDirFS, workingDir, err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("path is not a directory: %s", searchDir)
			}

			fsys := root.FS()

			type fileResult struct {
				path    string
				modTime time.Time
			}
			var results []fileResult

			err = walkWorkspace(ctx, fsys, searchDirFS, func(path, relPath string) error {
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

			totalMatches := len(results)

			if totalMatches == 0 {
				return "No files found matching pattern", nil
			}

			// Sort by modification time (newest first). The walk visits every match
			// before sorting so that the "newest" promise actually holds — a top-N
			// from a partial walk would just be the newest among the first files
			// visited, which depends on filesystem order.
			sort.Slice(results, func(i, j int) bool {
				return results[i].modTime.After(results[j].modTime)
			})

			resultLimitReached := false
			if totalMatches > limit {
				results = results[:limit]
				resultLimitReached = true
			}

			paths := make([]string, len(results))
			for i, r := range results {
				paths[i] = r.path
			}

			rawOutput := strings.Join(paths, "\n")
			truncatedOutput, truncated := truncateHead(rawOutput)

			duration := time.Since(startTime)

			var notices []string

			if resultLimitReached {
				notices = append(notices, fmt.Sprintf("%d files found, showing newest %d in %dms", totalMatches, limit, duration.Milliseconds()))
				notices = append(notices, "refine the pattern or raise limit for more results")
			} else {
				notices = append(notices, fmt.Sprintf("%d files found in %dms", totalMatches, duration.Milliseconds()))
			}

			if truncated {
				notices = append(notices, fmt.Sprintf("%dKB limit reached", DefaultMaxBytes/1024))
			}

			truncatedOutput += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))

			return truncatedOutput, nil
		},
	}
}
