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

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const (
	DefaultGrepLimit     = 100
	DefaultScanBufSize   = 64 * 1024   // 64KB initial buffer
	MaxScanBufSize       = 1024 * 1024 // 1MB max buffer for long lines
	MaxLineDisplayLength = 200
)

func GrepTool() tool.Tool {
	return tool.Tool{
		Name: "grep",

		Description: strings.Join([]string{
			fmt.Sprintf("Search file contents for a pattern. Returns matching lines with path and line number. Respects .gitignore. Truncated to %d matches or %dKB.", DefaultGrepLimit, DefaultMaxBytes/1024),
			"",
			"Usage:",
			"- ALWAYS use this tool for content search. NEVER run grep or rg via the shell tool.",
			"- Supports full regex syntax (e.g., \"log.*Error\", \"func\\s+\\w+\").",
			"- Filter files with the glob parameter (e.g., \"*.go\", \"*.{ts,tsx}\").",
			"- Use literal=true for strings containing regex special characters.",
			"- Use context for surrounding lines when you need to understand the match.",
			"- Use this to locate relevant code before reading entire files with `read`.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Search pattern (regex by default, or literal string with literal=true)",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in (defaults to working directory)",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "Glob pattern to filter files (e.g., \"*.go\", \"*.{ts,tsx}\")",
				},
				"ignoreCase": map[string]any{
					"type":        "boolean",
					"description": "Case-insensitive search (default: false)",
				},
				"literal": map[string]any{
					"type":        "boolean",
					"description": "Treat pattern as literal string, not regex (default: false)",
				},
				"context": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show before and after each match (default: 0)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of matches to return (default: 100)",
				},
				"output_mode": map[string]any{
					"type":        "string",
					"description": "Output format: \"content\" shows matching lines (default), \"files_with_matches\" shows only file paths, \"count\" shows match counts per file.",
					"enum":        []string{"content", "files_with_matches", "count"},
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

			searchPathFS, err := ensurePathInWorkspaceFS(searchPath, workingDir, "search")

			if err != nil {
				return "", err
			}

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

			literal := false

			if l, ok := args["literal"].(bool); ok {
				literal = l
			}

			outputMode := "content"

			if m, ok := args["output_mode"].(string); ok && m != "" {
				outputMode = m
			}

			// Compile regex
			regexPattern := pattern
			if literal {
				regexPattern = regexp.QuoteMeta(pattern)
			}
			if ignoreCase {
				regexPattern = "(?i)" + regexPattern
			}
			re, err := regexp.Compile(regexPattern)

			if err != nil {
				return "", fmt.Errorf("invalid regex pattern: %w", err)
			}

			// Check if path exists
			info, err := env.Root.Stat(searchPathFS)

			if err != nil {
				return "", pathError("stat path", searchPath, searchPathFS, workingDir, err)
			}

			fsys := env.Root.FS()

			// If path is a file, search just that file
			if !info.IsDir() {
				matches := searchFileWithContext(fsys, searchPathFS, re, contextLines, limit)

				if len(matches) == 0 {
					return "No matches found", nil
				}

				if outputMode == "files_with_matches" {
					return filepath.FromSlash(searchPathFS), nil
				}

				if outputMode == "count" {
					return fmt.Sprintf("%s:%d", filepath.FromSlash(searchPathFS), len(matches)), nil
				}

				return strings.Join(matches, "\n"), nil
			}

			var results []string
			matchCount := 0
			limitReached := false

			// For files_with_matches and count modes, track per-file data
			type fileMatch struct {
				path  string
				count int
			}
			var fileMatches []fileMatch

			err = walkWorkspace(ctx, fsys, searchPathFS, func(path, relPath string) error {
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

				// For files_with_matches mode, just check if file has any match
				if outputMode == "files_with_matches" {
					matches := searchFileWithContext(fsys, path, re, 0, 1)
					if len(matches) > 0 {
						fileMatches = append(fileMatches, fileMatch{path: filepath.FromSlash(relPath)})
						matchCount++

						if matchCount >= limit {
							limitReached = true
							return filepath.SkipAll
						}
					}
					return nil
				}

				// For count mode, count all matches in file
				if outputMode == "count" {
					matches := searchFileWithContext(fsys, path, re, 0, 10000)
					if len(matches) > 0 {
						fileMatches = append(fileMatches, fileMatch{path: filepath.FromSlash(relPath), count: len(matches)})
						matchCount++

						if matchCount >= limit {
							limitReached = true
							return filepath.SkipAll
						}
					}
					return nil
				}

				// Content mode: full results with context
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

			// Build output based on mode
			var output string

			switch outputMode {
			case "files_with_matches":
				if len(fileMatches) == 0 {
					return "No matches found", nil
				}
				paths := make([]string, len(fileMatches))
				for i, fm := range fileMatches {
					paths[i] = fm.path
				}
				output = strings.Join(paths, "\n")

			case "count":
				if len(fileMatches) == 0 {
					return "No matches found", nil
				}
				lines := make([]string, len(fileMatches))
				for i, fm := range fileMatches {
					lines[i] = fmt.Sprintf("%s:%d", fm.path, fm.count)
				}
				output = strings.Join(lines, "\n")

			default: // "content"
				if len(results) == 0 {
					return "No matches found", nil
				}
				output = strings.Join(results, "\n")
			}

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
	buf := make([]byte, 0, DefaultScanBufSize)
	scanner.Buffer(buf, MaxScanBufSize)

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

			if len(lineContent) > MaxLineDisplayLength {
				lineContent = lineContent[:MaxLineDisplayLength-3] + "..."
			}

			results = append(results, fmt.Sprintf("%s:%d:%s %s", displayPath, j+1, prefix, lineContent))
			lastPrinted = j
		}
	}

	return results
}
