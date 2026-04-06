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

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const (
	DefaultGrepLimit     = 200
	DefaultScanBufSize   = 64 * 1024   // 64KB initial buffer
	MaxScanBufSize       = 1024 * 1024 // 1MB max buffer for long lines
	MaxLineDisplayLength = 200
)

func GrepTool() tool.Tool {
	return tool.Tool{
		Name: "grep",

		Description: strings.Join([]string{
			fmt.Sprintf("Search file contents for a pattern. Respects .gitignore. Default limit: %d matches.", DefaultGrepLimit),
			"",
			"Usage:",
			"- ALWAYS use this tool for content search. NEVER run grep or rg via the shell.",
			"- Supports regex (e.g., \"log.*Error\", \"func\\s+\\w+\"). Literal braces need escaping (use `interface\\{\\}` to find `interface{}`).",
			"- Filter files with glob (e.g., \"*.go\", \"*.{ts,tsx}\").",
			"- Use literal=true for strings with regex special characters.",
			"- Output modes: \"content\" shows matching lines (default), \"files_with_matches\" shows only file paths, \"count\" shows match counts per file.",
			"- Use multiline=true for patterns spanning multiple lines (e.g., multi-line function signatures, struct definitions).",
			"- Use head_limit and offset to paginate large result sets.",
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
				"multiline": map[string]any{
					"type":        "boolean",
					"description": "Enable multiline mode where . matches newlines and patterns can span lines (default: false)",
				},
				"context": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show before and after each match (default: 0)",
				},
				"before_context": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show before each match (-B). Overrides context for before.",
				},
				"after_context": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show after each match (-A). Overrides context for after.",
				},
				"head_limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum number of results to return (default: %d)", DefaultGrepLimit),
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Skip first N results before applying head_limit, for pagination (default: 0)",
				},
				"output_mode": map[string]any{
					"type":        "string",
					"description": "Output format: \"content\" shows matching lines (default), \"files_with_matches\" shows only file paths, \"count\" shows match counts per file.",
					"enum":        []string{"content", "files_with_matches", "count"},
				},
			},
			"required": []string{"pattern"},
		},

		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			pattern, ok := args["pattern"].(string)

			if !ok || pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}

			searchPath := "."

			if p, ok := args["path"].(string); ok && p != "" {
				searchPath = p
			}

			workingDir := env.RootDir()

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

			multiline := false

			if ml, ok := args["multiline"].(bool); ok {
				multiline = ml
			}

			contextLines := 0
			beforeContext := 0
			afterContext := 0

			if c, ok := args["context"].(float64); ok && c > 0 {
				contextLines = int(c)
				beforeContext = contextLines
				afterContext = contextLines
			}

			if bc, ok := args["before_context"].(float64); ok && bc > 0 {
				beforeContext = int(bc)
			}

			if ac, ok := args["after_context"].(float64); ok && ac > 0 {
				afterContext = int(ac)
			}

			headLimit := DefaultGrepLimit

			if l, ok := args["head_limit"].(float64); ok && l > 0 {
				headLimit = int(l)
			}
			// Support legacy "limit" parameter
			if l, ok := args["limit"].(float64); ok && l > 0 {
				headLimit = int(l)
			}

			resultOffset := 0

			if o, ok := args["offset"].(float64); ok && o > 0 {
				resultOffset = int(o)
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

			flags := ""
			if ignoreCase {
				flags += "i"
			}
			if multiline {
				flags += "s" // dotall: . matches \n
			}
			if flags != "" {
				regexPattern = "(?" + flags + ")" + regexPattern
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
				matches := searchFileWithContext(fsys, searchPathFS, re, beforeContext, afterContext, headLimit+resultOffset)

				if len(matches) == 0 {
					return "No matches found", nil
				}

				// Apply offset
				if resultOffset > 0 {
					if resultOffset >= len(matches) {
						return "No matches found (offset beyond results)", nil
					}
					matches = matches[resultOffset:]
				}
				if len(matches) > headLimit {
					matches = matches[:headLimit]
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
			skippedCount := 0
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
					matches := searchFileWithContext(fsys, path, re, 0, 0, 1)
					if len(matches) > 0 {
						matchCount++

						if matchCount <= resultOffset {
							skippedCount++
							return nil
						}

						fileMatches = append(fileMatches, fileMatch{path: filepath.FromSlash(relPath)})

						if len(fileMatches) >= headLimit {
							limitReached = true
							return filepath.SkipAll
						}
					}
					return nil
				}

				// For count mode, count all matches in file
				if outputMode == "count" {
					matches := searchFileWithContext(fsys, path, re, 0, 0, 10000)
					if len(matches) > 0 {
						matchCount++

						if matchCount <= resultOffset {
							skippedCount++
							return nil
						}

						fileMatches = append(fileMatches, fileMatch{path: filepath.FromSlash(relPath), count: len(matches)})

						if len(fileMatches) >= headLimit {
							limitReached = true
							return filepath.SkipAll
						}
					}
					return nil
				}

				// Content mode: full results with context
				remaining := headLimit - len(results) + resultOffset - skippedCount

				if remaining <= 0 {
					limitReached = true

					return filepath.SkipAll
				}

				matches := searchFileWithContext(fsys, path, re, beforeContext, afterContext, remaining)

				for _, m := range matches {
					matchCount++

					if matchCount <= resultOffset {
						skippedCount++
						continue
					}

					results = append(results, m)

					if len(results) >= headLimit {
						limitReached = true
						return filepath.SkipAll
					}
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

			output, truncated := truncateHead(output)

			var notices []string

			if limitReached {
				notices = append(notices, fmt.Sprintf("%d results limit reached", headLimit))
				if resultOffset == 0 {
					notices = append(notices, fmt.Sprintf("use offset=%d to see more", headLimit))
				}
			}

			if truncated {
				notices = append(notices, fmt.Sprintf("%dKB limit reached", DefaultMaxBytes/1024))
			}

			if len(notices) > 0 {
				output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			return output, nil
		},
	}
}

func searchFileWithContext(fsys fs.FS, path string, re *regexp.Regexp, beforeContext, afterContext, limit int) []string {
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

		start := max(0, i-beforeContext)
		end := min(len(lines)-1, i+afterContext)

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
