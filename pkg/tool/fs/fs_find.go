package fs

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

const DefaultFindLimit = 1000

var defaultIgnoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".svn":         true,
	"__pycache__":  true,
	".venv":        true,
	"vendor":       true,
}

func FindTool() tool.Tool {
	return tool.Tool{
		Name: "find",

		Description: fmt.Sprintf(
			"Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore files and common ignore patterns (node_modules, .git, etc). Output is truncated to %d results or %dKB (whichever is hit first).",
			DefaultFindLimit,
			DefaultMaxBytes/1024,
		),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files (e.g. *.go, **/*.txt)"},
				"path":    map[string]any{"type": "string", "description": "Directory to search in (defaults to current directory)"},
				"limit":   map[string]any{"type": "integer", "description": "Maximum number of results to return"},
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

			if isOutsideWorkspace(searchDir, workingDir) {
				return "", fmt.Errorf("cannot search: path %q is outside workspace %q", searchDir, workingDir)
			}

			searchDir = normalizePath(searchDir, workingDir)

			limit := DefaultFindLimit
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			info, err := env.Root.Stat(searchDir)
			if err != nil {
				return "", pathError("stat path", searchDir, searchDir, workingDir, err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("path is not a directory: %s", searchDir)
			}

			fsys := env.Root.FS()

			var allPatterns []gitignore.Pattern
			allPatterns = append(allPatterns, loadGitignore(fsys, nil)...)

			matcher := gitignore.NewMatcher(allPatterns)

			var results []string
			resultLimitReached := false

			err = fs.WalkDir(fsys, searchDir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}

				if d.IsDir() && defaultIgnoreDirs[d.Name()] {
					return filepath.SkipDir
				}

				relPath := path
				if searchDir != "." {
					relPath, _ = filepath.Rel(searchDir, path)
				}

				pathParts := strings.Split(filepath.ToSlash(relPath), "/")

				if d.IsDir() {
					if matcher.Match(pathParts, true) {
						return filepath.SkipDir
					}

					newPatterns := loadGitignore(fsys, strings.Split(path, string(filepath.Separator)))
					if len(newPatterns) > 0 {
						allPatterns = append(allPatterns, newPatterns...)
						matcher = gitignore.NewMatcher(allPatterns)
					}

					return nil
				}

				if matcher.Match(pathParts, false) {
					return nil
				}

				if len(results) >= limit {
					resultLimitReached = true
					return filepath.SkipAll
				}

				matchPath := filepath.ToSlash(relPath)

				matched, err := doublestar.Match(pattern, matchPath)
				if err != nil {
					return nil
				}

				if matched {
					results = append(results, relPath)
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

func loadGitignore(fsys fs.FS, domain []string) []gitignore.Pattern {
	gitignorePath := ".gitignore"
	if len(domain) > 0 {
		gitignorePath = filepath.Join(append(domain, ".gitignore")...)
	}

	f, err := fsys.Open(gitignorePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}

	return patterns
}
