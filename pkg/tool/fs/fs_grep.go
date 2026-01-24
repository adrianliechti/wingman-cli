package fs

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

const DefaultGrepLimit = 100

func GrepTool() tool.Tool {
	return tool.Tool{
		Name: "grep",

		Description: fmt.Sprintf(
			"Search file contents for a pattern (regex or literal). Returns matching lines with file path and line number. Respects .gitignore. Output truncated to %d matches or %dKB.",
			DefaultGrepLimit,
			DefaultMaxBytes/1024,
		),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Search pattern (supports regex)",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search (defaults to current directory)",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "File pattern to filter (e.g., *.go, *.ts)",
				},
				"ignoreCase": map[string]any{
					"type":        "boolean",
					"description": "Case-insensitive search (default: false)",
				},
				"context": map[string]any{
					"type":        "integer",
					"description": "Lines of context around matches (default: 0)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of matches to return",
				},
			},
			"required": []string{"pattern"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			pattern, ok := args["pattern"].(string)

			if !ok || pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}

			searchPath := "."

			if p, ok := args["path"].(string); ok && p != "" {
				searchPath = p
			}

			workingDir := env.WorkingDir()

			if isOutsideWorkspace(searchPath, workingDir) {
				return "", fmt.Errorf("cannot search: path %q is outside workspace %q", searchPath, workingDir)
			}

			searchPathFS := normalizePathFS(searchPath, workingDir)

			glob := ""

			if g, ok := args["glob"].(string); ok {
				glob = g
			}

			ignoreCase := false

			if ic, ok := args["ignoreCase"].(bool); ok {
				ignoreCase = ic
			}

			contextLines := 0

			if c, ok := args["context"].(float64); ok && c > 0 {
				contextLines = int(c)
			}

			limit := DefaultGrepLimit

			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			// Compile regex
			if ignoreCase {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)

			if err != nil {
				return "", fmt.Errorf("invalid regex pattern: %w", err)
			}

			// Check if path exists
			info, err := env.Root.Stat(searchPath)

			if err != nil {
				return "", fmt.Errorf("path not found: %s", searchPath)
			}

			fsys := env.Root.FS()

			// If path is a file, search just that file
			if !info.IsDir() {
				matches := searchFileWithContext(fsys, searchPathFS, re, contextLines, limit)

				if len(matches) == 0 {
					return "No matches found", nil
				}

				return strings.Join(matches, "\n"), nil
			}

			// Load gitignore patterns
			var allPatterns []gitignore.Pattern
			allPatterns = append(allPatterns, loadGitignore(fsys, nil)...)
			matcher := gitignore.NewMatcher(allPatterns)

			var results []string
			matchCount := 0
			limitReached := false

			err = fs.WalkDir(fsys, searchPathFS, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}

				// Skip ignored directories
				if d.IsDir() {
					if defaultIgnoreDirs[d.Name()] {
						return filepath.SkipDir
					}

					relPath := path

					if searchPathFS != "." {
						relPath = relPathSlash(searchPathFS, path)
					}
					pathParts := strings.Split(relPath, "/")

					if matcher.Match(pathParts, true) {
						return filepath.SkipDir
					}

					// Load nested gitignore
					newPatterns := loadGitignore(fsys, pathDomain(path))

					if len(newPatterns) > 0 {
						allPatterns = append(allPatterns, newPatterns...)
						matcher = gitignore.NewMatcher(allPatterns)
					}

					return nil
				}

				// Check gitignore for files
				relPath := path

				if searchPathFS != "." {
					relPath = relPathSlash(searchPathFS, path)
				}
				pathParts := strings.Split(relPath, "/")

				if matcher.Match(pathParts, false) {
					return nil
				}

				// Check glob pattern
				if glob != "" {
					matched, _ := doublestar.Match(glob, pathpkg.Base(path))

					if !matched {
						// Also try matching against the full relative path
						matched, _ = doublestar.Match(glob, relPath)

						if !matched {
							return nil
						}
					}
				}

				// Skip binary files (simple heuristic: check extension)
				if isBinaryFile(path) {
					return nil
				}

				// Search file
				remaining := limit - matchCount

				if remaining <= 0 {
					limitReached = true

					return filepath.SkipAll
				}

				matches := searchFileWithContext(fsys, path, re, contextLines, remaining)

				if len(matches) > 0 {
					results = append(results, matches...)
					matchCount += len(matches)
				}

				return nil
			})

			if err != nil && err != filepath.SkipAll {
				return "", fmt.Errorf("search failed: %w", err)
			}

			if len(results) == 0 {
				return "No matches found", nil
			}

			output := strings.Join(results, "\n")
			output, _, bytesTruncated := truncateHead(output)

			var notices []string

			if limitReached || matchCount >= limit {
				notices = append(notices, fmt.Sprintf("%d matches limit reached", limit))
			}

			if bytesTruncated {
				notices = append(notices, fmt.Sprintf("%dKB limit reached", DefaultMaxBytes/1024))
			}

			if len(notices) > 0 {
				output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			return output, nil
		},
	}
}

func searchFileWithContext(fsys fs.FS, path string, re *regexp.Regexp, contextLines, limit int) []string {
	f, err := fsys.Open(path)

	if err != nil {
		return nil
	}
	defer f.Close()

	displayPath := filepath.FromSlash(path)

	var lines []string
	scanner := bufio.NewScanner(f)

	// Use a reasonable buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if scanner.Err() != nil {
		return nil
	}

	var results []string
	matchedLines := make(map[int]bool)

	// First pass: find all matching lines
	for i, line := range lines {
		if re.MatchString(line) {
			matchedLines[i] = true
		}
	}

	if len(matchedLines) == 0 {
		return nil
	}

	// Second pass: collect results with context
	printed := make(map[int]bool)
	lastPrinted := -2

	for i := range lines {
		if !matchedLines[i] {
			continue
		}

		// Check if we've hit the limit
		if len(results) >= limit {
			break
		}

		start := max(0, i-contextLines)
		end := min(len(lines)-1, i+contextLines)

		// Add separator if there's a gap
		if lastPrinted >= 0 && start > lastPrinted+1 {
			results = append(results, "--")
		}

		for j := start; j <= end; j++ {
			if printed[j] {
				continue
			}
			printed[j] = true

			prefix := " "

			if matchedLines[j] {
				prefix = ">"
			}

			// Format: path:linenum:prefix:content
			lineContent := lines[j]

			if len(lineContent) > 200 {
				lineContent = lineContent[:197] + "..."
			}

			results = append(results, fmt.Sprintf("%s:%d:%s %s", displayPath, j+1, prefix, lineContent))
			lastPrinted = j
		}
	}

	return results
}