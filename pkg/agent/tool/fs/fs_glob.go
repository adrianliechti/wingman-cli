package fs

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const DefaultGlobLimit = 100

func GlobTool(root *os.Root, allowedReadRoots ...string) tool.Tool {
	return tool.Tool{
		Name:   "glob",
		Effect: tool.StaticEffect(tool.EffectReadOnly),

		Description: strings.Join([]string{
			"- Fast file pattern matching tool that works with any codebase size.",
			"- Supports glob patterns like `**/*.js` or `src/**/*.ts`.",
			"- Returns matching file paths sorted by modification time (newest first).",
			"- Use this tool when you need to find files by name patterns. Use `grep` for content/symbols.",
			"- Symlinks and version-control directories (`.git`, `.svn`, …) are skipped. All other files (including dotfiles) are listed; exclude them with a more specific pattern.",
			"- For open-ended searches requiring multiple rounds, use the `agent` tool.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "The glob pattern to match files against."},
				"path":    map[string]any{"type": "string", "description": "The directory to search in. Defaults to workspace root. Must be a valid directory path if provided."},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			pattern, ok := args["pattern"].(string)

			if !ok || strings.TrimSpace(pattern) == "" {
				return tool.Result{}, fmt.Errorf("pattern is required")
			}
			searchDir := "."

			if p, ok := args["path"].(string); ok && p != "" {
				searchDir = p
			}

			workingDir := root.Name()

			target, pattern, err := resolveGlobSearch(searchDir, pattern, workingDir, root, allowedReadRoots)
			if err != nil {
				return tool.Result{}, err
			}
			defer target.Close()

			if _, err := doublestar.Match(pattern, ""); err != nil {
				return tool.Result{}, fmt.Errorf("invalid glob pattern: %w", err)
			}

			info, err := target.Root.Stat(target.SearchDirFS)

			if err != nil {
				return tool.Result{}, fmt.Errorf("stat path %q: %w", searchDir, err)
			}

			if !info.IsDir() {
				return tool.Result{}, fmt.Errorf("path is not a directory: %s", searchDir)
			}

			fsys := vcsFilteredFS{target.Root.FS()}

			type fileResult struct {
				path    string
				modTime time.Time
			}
			var results []fileResult

			fullPattern := pattern
			if target.SearchDirFS != "." && target.SearchDirFS != "" {
				fullPattern = target.SearchDirFS + "/" + pattern
			}

			err = doublestar.GlobWalk(fsys, fullPattern, func(p string, d fs.DirEntry) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				results = append(results, fileResult{path: target.ReportPath(p), modTime: entryModTime(d)})
				return nil
			}, doublestar.WithFilesOnly(), doublestar.WithNoFollow())

			if err != nil && err != filepath.SkipAll {
				return tool.Result{}, fmt.Errorf("failed to search directory: %w", err)
			}

			totalMatches := len(results)

			if totalMatches == 0 {
				return tool.Text("No files found"), nil
			}

			slices.SortFunc(results, func(a, b fileResult) int {
				return cmp.Or(b.modTime.Compare(a.modTime), cmp.Compare(a.path, b.path))
			})

			end := totalMatches
			resultLimitReached := false
			if DefaultGlobLimit < end {
				end = DefaultGlobLimit
				resultLimitReached = true
			}
			results = results[:end]

			paths := make([]string, len(results))
			for i, r := range results {
				paths[i] = r.path
			}

			output := strings.Join(paths, "\n")

			if resultLimitReached {
				output += "\n(Results are truncated. Consider using a more specific path or pattern.)"
			}

			return tool.Text(output), nil
		},
	}
}

func resolveGlobSearch(searchDir, pattern, workingDir string, workspaceRoot *os.Root, allowedReadRoots []string) (*searchTarget, string, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))

	if filepath.IsAbs(pattern) {
		dir, rest := doublestar.SplitPattern(pattern)
		target, err := resolveSearchTarget(filepath.FromSlash(dir), workingDir, workspaceRoot, allowedReadRoots, "search")
		if err != nil {
			return nil, "", err
		}
		return target, normalizeGlobPattern(rest), nil
	}

	target, err := resolveSearchTarget(searchDir, workingDir, workspaceRoot, allowedReadRoots, "search")
	if err != nil {
		return nil, "", err
	}
	return target, normalizeGlobPattern(pattern), nil
}

type vcsFilteredFS struct{ fs.FS }

func (f vcsFilteredFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(f.FS, name)
	if err != nil {
		return nil, err
	}
	filtered := entries[:0]
	for _, e := range entries {
		if e.IsDir() && vcsDirs[e.Name()] {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

func normalizeGlobPattern(pattern string) string {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" || strings.Contains(pattern, "/") {
		return pattern
	}
	return "**/" + pattern
}
