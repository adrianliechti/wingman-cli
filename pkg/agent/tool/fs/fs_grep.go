package fs

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const (
	DefaultGrepLimit     = 250
	DefaultScanBufSize   = 64 * 1024
	MaxScanBufSize       = 1024 * 1024
	MaxLineDisplayLength = 500
)

type grepArgs struct {
	searchPath      string
	globPatterns    []string
	typeFilter      string
	multiline       bool
	showLineNumbers bool
	beforeContext   int
	afterContext    int
	headLimit       int
	unlimited       bool
	effectiveLimit  int
	resultOffset    int
	outputMode      string
	re              *regexp.Regexp
}

func parseGrepArgs(args map[string]any) (*grepArgs, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	searchPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = p
	}

	var globPatterns []string
	if g, ok := args["glob"].(string); ok {
		globPatterns = splitGrepGlobs(g)
	}
	for _, glob := range globPatterns {
		if _, err := doublestar.Match(strings.TrimPrefix(glob, "!"), ""); err != nil {
			return nil, fmt.Errorf("invalid glob pattern: %w", err)
		}
	}

	typeFilter := ""
	if t, ok := args["type"].(string); ok {
		typeFilter = strings.ToLower(strings.TrimSpace(t))
	}
	if typeFilter != "" && !validGrepType(typeFilter) {
		return nil, fmt.Errorf("unsupported type %q (supported: %s)", typeFilter, strings.Join(supportedGrepTypes(), ", "))
	}

	ignoreCase, _ := args["-i"].(bool)
	multiline, _ := args["multiline"].(bool)
	showLineNumbers := true
	if n, ok := args["-n"].(bool); ok {
		showLineNumbers = n
	}

	beforeContext, afterContext := 0, 0
	for _, key := range []string{"context", "-C"} {
		if v, present, err := tool.NonNegIntArg(args, key); present {
			if err != nil {
				return nil, err
			}
			beforeContext, afterContext = v, v
		}
	}
	if v, present, err := tool.NonNegIntArg(args, "-B"); present {
		if err != nil {
			return nil, err
		}
		beforeContext = v
	}
	if v, present, err := tool.NonNegIntArg(args, "-A"); present {
		if err != nil {
			return nil, err
		}
		afterContext = v
	}

	headLimit := DefaultGrepLimit
	if v, present, err := tool.NonNegIntArg(args, "head_limit"); present {
		if err != nil {
			return nil, err
		}
		headLimit = v
	}

	resultOffset := 0
	for _, key := range []string{"skip", "offset"} {
		if v, present, err := tool.NonNegIntArg(args, key); present {
			if err != nil {
				return nil, err
			}
			resultOffset = v
			break
		}
	}

	outputMode := "files_with_matches"
	if m, ok := args["output_mode"].(string); ok && m != "" {
		outputMode = m
	}
	if !validGrepOutputMode(outputMode) {
		return nil, fmt.Errorf("output_mode must be content, files_with_matches, or count")
	}

	regexPattern := pattern
	flags := ""
	if ignoreCase {
		flags += "i"
	}
	if multiline {
		flags += "s"
	}
	if flags != "" {
		regexPattern = "(?" + flags + ")" + regexPattern
	}
	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}

	unlimited := headLimit == 0
	effectiveLimit := headLimit
	if unlimited {
		effectiveLimit = math.MaxInt
	}

	return &grepArgs{
		searchPath:      searchPath,
		globPatterns:    globPatterns,
		typeFilter:      typeFilter,
		multiline:       multiline,
		showLineNumbers: showLineNumbers,
		beforeContext:   beforeContext,
		afterContext:    afterContext,
		headLimit:       headLimit,
		unlimited:       unlimited,
		effectiveLimit:  effectiveLimit,
		resultOffset:    resultOffset,
		outputMode:      outputMode,
		re:              re,
	}, nil
}

func GrepTool(root *os.Root, allowedReadRoots ...string) tool.Tool {
	return tool.Tool{
		Name:   "grep",
		Effect: tool.StaticEffect(tool.EffectReadOnly),

		Description: strings.Join([]string{
			"A powerful content search tool with ripgrep-style options.",
			"- Supports regex syntax (e.g. `log.*Error`, `function\\s+\\w+`).",
			"- Pattern syntax is Go RE2 (not POSIX grep or PCRE): no lookaheads/lookbehinds or backreferences. Escape literal braces (use `interface\\{\\}` to find `interface{}`).",
			"- Filter files with `glob` (e.g. `*.js`, `*.{ts,tsx}`) or `type` (e.g. `js`, `py`, `rust`). Use `path` to scope to a subtree or single file.",
			"- Output modes: `files_with_matches` (default) shows file paths, `content` shows matching lines (supports `-A`/`-B`/`-C` context and `-n`), `count` shows match counts.",
			"- Multiline matching: by default patterns match within single lines only. For cross-line patterns like `struct \\{[\\s\\S]*?field`, set `multiline=true`.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Regular expression to search file contents for."},
				"path":    map[string]any{"type": "string", "description": "File or directory to search. Defaults to workspace."},
				"glob":    map[string]any{"type": "string", "description": "Glob filter for files (e.g. `*.js`, `*.{ts,tsx}`)."},
				"type":    map[string]any{"type": "string", "description": "File type filter (js, py, rust, go, java, …)."},
				"output_mode": map[string]any{
					"type":        "string",
					"description": "`content` shows matching lines; `files_with_matches` (default) shows paths; `count` shows match counts.",
					"enum":        []string{"content", "files_with_matches", "count"},
					"default":     "files_with_matches",
				},
				"-B": map[string]any{"type": "integer", "description": "Lines shown before each match (content mode)."},
				"-A": map[string]any{"type": "integer", "description": "Lines shown after each match (content mode)."},
				"-C": map[string]any{"type": "integer", "description": "Lines shown before and after each match (content mode)."},
				"-n": map[string]any{"type": "boolean", "description": "Show line numbers (content mode). Defaults to true.", "default": true},
				"-i": map[string]any{"type": "boolean", "description": "Case-insensitive search.", "default": false},
				"head_limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("First N entries, any mode. Defaults to %d; 0 = unlimited (sparingly).", DefaultGrepLimit),
					"default":     DefaultGrepLimit,
				},
				"skip":      map[string]any{"type": "integer", "description": "Skip the first N result entries (pagination, not a line number).", "default": 0},
				"multiline": map[string]any{"type": "boolean", "description": "Let patterns span lines (`.` matches newlines).", "default": false},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			workingDir := root.Name()
			cfg, err := parseGrepArgs(args)
			if err != nil {
				return tool.Result{}, err
			}

			target, err := resolveSearchTarget(cfg.searchPath, workingDir, root, allowedReadRoots, "search")
			if err != nil {
				return tool.Result{}, err
			}
			defer target.Close()

			searchPath := cfg.searchPath
			searchPathFS := target.SearchDirFS
			globPatterns := cfg.globPatterns
			typeFilter := cfg.typeFilter
			multiline := cfg.multiline
			showLineNumbers := cfg.showLineNumbers
			beforeContext, afterContext := cfg.beforeContext, cfg.afterContext
			headLimit := cfg.headLimit
			unlimited := cfg.unlimited
			effectiveLimit := cfg.effectiveLimit
			resultOffset := cfg.resultOffset
			outputMode := cfg.outputMode
			re := cfg.re

			info, err := target.Root.Stat(searchPathFS)

			if err != nil {
				return tool.Result{}, fmt.Errorf("stat path %q: %w", searchPath, err)
			}

			fsys := target.Root.FS()

			if !info.IsDir() {
				if typeFilter != "" && !matchesType(searchPathFS, typeFilter) {
					return tool.Text("No matches found"), nil
				}
				if !matchesGrepGlobs(globPatterns, searchPathFS, searchPathFS) {
					return tool.Text("No matches found"), nil
				}

				reportPath := target.ReportPath(searchPathFS)

				if outputMode == "count" {
					count := countFileMatches(fsys, searchPathFS, re, multiline)
					if count == 0 || resultOffset > 0 {
						return tool.Text("No matches found"), nil
					}
					occurrences := "occurrences"
					if count == 1 {
						occurrences = "occurrence"
					}
					return tool.Text(fmt.Sprintf("%s:%d\n\nFound %d total %s across 1 file.", reportPath, count, count, occurrences)), nil
				}

				if outputMode == "files_with_matches" {
					matches := searchFileWithContext(fsys, searchPathFS, reportPath, re, 0, 0, 1, multiline, true)
					if len(matches) == 0 {
						return tool.Text("No files found"), nil
					}
					if resultOffset > 0 {
						return tool.Text("No files found"), nil
					}
					return tool.Text(fmt.Sprintf("Found 1 file\n%s", reportPath)), nil
				}

				searchLimit := effectiveLimit
				if !unlimited {
					searchLimit = resultOffset + headLimit + 1
				}
				matches := searchFileWithContext(fsys, searchPathFS, reportPath, re, beforeContext, afterContext, searchLimit, multiline, showLineNumbers)

				if len(matches) == 0 {
					return tool.Text("No matches found"), nil
				}

				if resultOffset > 0 {
					if resultOffset >= len(matches) {
						return tool.Text("No matches found"), nil
					}
					matches = matches[resultOffset:]
				}
				resultLimitReached := false
				if !unlimited && len(matches) > headLimit {
					matches = matches[:headLimit]
					resultLimitReached = true
				}

				output := strings.Join(matches, "\n")
				if resultLimitReached || resultOffset > 0 {
					if notice := formatGrepPaginationNotice(resultLimitReached, headLimit, resultOffset); notice != "" {
						output += "\n\n[" + notice + "]"
					}
				}

				return tool.Text(output), nil
			}

			var results []string
			matchCount := 0
			limitReached := false

			type fileMatch struct {
				path    string
				count   int
				modTime time.Time
			}
			var fileMatches []fileMatch

			err = walkGrepFiles(ctx, fsys, searchPathFS, func(path, relPath string, d fs.DirEntry) error {
				if !matchesGrepGlobs(globPatterns, path, relPath) {
					return nil
				}

				if typeFilter != "" && !matchesType(path, typeFilter) {
					return nil
				}

				display := target.ReportPath(path)

				if outputMode == "files_with_matches" {
					matches := searchFileWithContext(fsys, path, display, re, 0, 0, 1, multiline, true)
					if len(matches) > 0 {
						fileMatches = append(fileMatches, fileMatch{path: display, modTime: entryModTime(d)})
					}
					return nil
				}

				if outputMode == "count" {
					count := countFileMatches(fsys, path, re, multiline)
					if count > 0 {
						fileMatches = append(fileMatches, fileMatch{path: display, count: count})
					}
					return nil
				}

				remaining := effectiveLimit
				if !unlimited {
					remaining = resultOffset + headLimit + 1 - matchCount
				}

				if !unlimited && remaining <= 0 {
					limitReached = true

					return filepath.SkipAll
				}

				matches := searchFileWithContext(fsys, path, display, re, beforeContext, afterContext, remaining, multiline, showLineNumbers)

				for _, m := range matches {
					matchCount++

					if matchCount <= resultOffset {
						continue
					}

					results = append(results, m)

					if !unlimited && len(results) > headLimit {
						limitReached = true
						return filepath.SkipAll
					}
				}

				return nil
			})

			if err != nil && err != filepath.SkipAll {
				return tool.Result{}, fmt.Errorf("search failed: %w", err)
			}

			var output string

			switch outputMode {
			case "files_with_matches":
				if len(fileMatches) == 0 {
					return tool.Text("No files found"), nil
				}

				slices.SortFunc(fileMatches, func(a, b fileMatch) int {
					return cmp.Or(b.modTime.Compare(a.modTime), cmp.Compare(a.path, b.path))
				})
				if resultOffset >= len(fileMatches) {
					return tool.Text("No files found"), nil
				}
				start := resultOffset
				end := len(fileMatches)
				if !unlimited && start+headLimit < end {
					end = start + headLimit
					limitReached = true
				}
				fileMatches = fileMatches[start:end]
				paths := make([]string, len(fileMatches))
				for i, fm := range fileMatches {
					paths[i] = fm.path
				}
				limitInfo := formatGrepLimitInfo(limitReached, headLimit, resultOffset)
				output = fmt.Sprintf("Found %d %s%s\n%s", len(paths), plural(len(paths), "file"), limitInfo, strings.Join(paths, "\n"))

			case "count":
				if len(fileMatches) == 0 {
					return tool.Text("No matches found"), nil
				}
				if resultOffset >= len(fileMatches) {
					return tool.Text("No matches found"), nil
				}
				start := resultOffset
				end := len(fileMatches)
				if !unlimited && start+headLimit < end {
					end = start + headLimit
					limitReached = true
				}
				fileMatches = fileMatches[start:end]
				lines := make([]string, len(fileMatches))
				totalMatches := 0
				for i, fm := range fileMatches {
					lines[i] = fmt.Sprintf("%s:%d", fm.path, fm.count)
					totalMatches += fm.count
				}
				output = strings.Join(lines, "\n")
				limitInfo := formatGrepLimitInfo(limitReached, headLimit, resultOffset)
				pagination := ""
				if limitInfo != "" {
					pagination = " with pagination =" + limitInfo
				}
				output += fmt.Sprintf("\n\nFound %d total %s across %d %s.%s", totalMatches, plural(totalMatches, "occurrence"), len(fileMatches), plural(len(fileMatches), "file"), pagination)

			default:
				if len(results) == 0 {
					return tool.Text("No matches found"), nil
				}
				if !unlimited && len(results) > headLimit {
					results = results[:headLimit]
					limitReached = true
				}
				output = strings.Join(results, "\n")
			}

			if outputMode == "content" && (limitReached || resultOffset > 0) {
				if notice := formatGrepPaginationNotice(limitReached, headLimit, resultOffset); notice != "" {
					output += "\n\n[" + notice + "]"
				}
			}

			return tool.Text(output), nil
		},
	}
}

func validGrepOutputMode(mode string) bool {
	switch mode {
	case "content", "files_with_matches", "count":
		return true
	default:
		return false
	}
}

func splitGrepGlobs(glob string) []string {
	var patterns []string
	for field := range strings.FieldsSeq(glob) {
		if strings.Contains(field, "{") && strings.Contains(field, "}") {
			patterns = append(patterns, field)
			continue
		}
		for part := range strings.SplitSeq(field, ",") {
			if part = strings.TrimSpace(part); part != "" {
				patterns = append(patterns, part)
			}
		}
	}
	return patterns
}

func matchesGrepGlobs(globs []string, path, relPath string) bool {
	if len(globs) == 0 {
		return true
	}

	matchedPositive := false
	hasPositive := false
	for _, glob := range globs {
		negated := strings.HasPrefix(glob, "!")
		pattern := strings.TrimPrefix(glob, "!")
		if pattern == "" {
			continue
		}

		matched := matchesSingleGrepGlob(pattern, path, relPath)
		if negated {
			if matched {
				return false
			}
			continue
		}

		hasPositive = true
		if matched {
			matchedPositive = true
		}
	}

	return !hasPositive || matchedPositive
}

func matchesSingleGrepGlob(pattern, path, relPath string) bool {
	if matched, _ := doublestar.Match(pattern, filepath.Base(path)); matched {
		return true
	}
	if matched, _ := doublestar.Match(pattern, filepath.ToSlash(relPath)); matched {
		return true
	}
	matched, _ := doublestar.Match(pattern, filepath.ToSlash(path))
	return matched
}

const binaryPeekSize = 512

func openTextFile(fsys fs.FS, path string) (io.ReadCloser, bool, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, false, err
	}

	var peek [binaryPeekSize]byte
	n, err := io.ReadFull(f, peek[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		f.Close()
		return nil, false, err
	}

	if isBinaryContent(peek[:n]) {
		f.Close()
		return nil, true, nil
	}

	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(peek[:n]), f), f}, false, nil
}

func countFileMatches(fsys fs.FS, path string, re *regexp.Regexp, multiline bool) int {
	rc, isBinary, err := openTextFile(fsys, path)
	if err != nil || isBinary {
		return 0
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, DefaultScanBufSize), MaxScanBufSize)

	if multiline {
		var b strings.Builder
		first := true
		for scanner.Scan() {
			if !first {
				b.WriteByte('\n')
			}
			first = false
			b.Write(scanner.Bytes())
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
			return 0
		}
		return len(re.FindAllStringIndex(b.String(), -1))
	}

	count := 0
	for scanner.Scan() {
		count += len(re.FindAllIndex(scanner.Bytes(), -1))
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return 0
	}
	return count
}

var grepTypeExtensions = map[string][]string{
	"c":          {".c"},
	"cpp":        {".cpp", ".cc", ".cxx", ".c++", ".hpp", ".hh", ".hxx", ".h++"},
	"cs":         {".cs"},
	"csharp":     {".cs"},
	"css":        {".css"},
	"dart":       {".dart"},
	"go":         {".go"},
	"h":          {".h", ".hpp", ".hh", ".hxx"},
	"html":       {".html", ".htm"},
	"java":       {".java"},
	"javascript": {".js", ".jsx", ".mjs", ".cjs"},
	"js":         {".js", ".jsx", ".mjs", ".cjs"},
	"json":       {".json"},
	"kotlin":     {".kt", ".kts"},
	"kt":         {".kt", ".kts"},
	"lua":        {".lua"},
	"markdown":   {".md", ".markdown"},
	"md":         {".md", ".markdown"},
	"php":        {".php"},
	"py":         {".py", ".pyw"},
	"python":     {".py", ".pyw"},
	"rb":         {".rb"},
	"ruby":       {".rb"},
	"rs":         {".rs"},
	"rust":       {".rs"},
	"scala":      {".scala", ".sc"},
	"sh":         {".sh", ".bash", ".zsh"},
	"sql":        {".sql"},
	"swift":      {".swift"},
	"toml":       {".toml"},
	"ts":         {".ts", ".mts", ".cts"},
	"tsx":        {".tsx"},
	"typescript": {".ts", ".mts", ".cts", ".tsx"},
	"vue":        {".vue"},
	"yaml":       {".yaml", ".yml"},
	"yml":        {".yaml", ".yml"},
}

func validGrepType(typeFilter string) bool {
	_, ok := grepTypeExtensions[typeFilter]
	return ok
}

func supportedGrepTypes() []string {
	types := make([]string, 0, len(grepTypeExtensions))
	for t := range grepTypeExtensions {
		types = append(types, t)
	}
	slices.Sort(types)
	return types
}

func matchesType(path, typeFilter string) bool {
	exts, ok := grepTypeExtensions[typeFilter]
	if !ok {
		return false
	}
	return slices.Contains(exts, strings.ToLower(filepath.Ext(path)))
}

func plural(n int, singular string) string {
	if n == 1 {
		return singular
	}
	return singular + "s"
}

func formatGrepLimitInfo(limitReached bool, limit, offset int) string {
	var parts []string
	if limitReached {
		parts = append(parts, fmt.Sprintf("limit: %d", limit))
	}
	if offset > 0 {
		parts = append(parts, fmt.Sprintf("skip: %d", offset))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, ", ")
}

func formatGrepPaginationNotice(limitReached bool, limit, offset int) string {
	info := strings.TrimSpace(formatGrepLimitInfo(limitReached, limit, offset))
	if info == "" {
		return ""
	}
	return "Showing results with pagination = " + info
}

func lineContaining(lineStarts []int, offset int) int {
	i, found := slices.BinarySearch(lineStarts, offset)
	if !found {
		i--
	}
	return max(0, i)
}

func entryModTime(d fs.DirEntry) time.Time {
	info, err := d.Info()
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func walkGrepFiles(ctx context.Context, fsys fs.FS, root string, onFile func(path, relPath string, d fs.DirEntry) error) error {
	cache := newGitignoreCache(fsys)

	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		if d.IsDir() {
			if vcsDirs[d.Name()] {
				return filepath.SkipDir
			}
			if cache.matches(path, true) {
				return filepath.SkipDir
			}
			return nil
		}

		if cache.matches(path, false) {
			return nil
		}

		return onFile(path, relPathFromBase(root, path), d)
	})
}

type gitignoreCache struct {
	fsys     fs.FS
	patterns map[string][]gitignore.Pattern
}

func newGitignoreCache(fsys fs.FS) *gitignoreCache {
	return &gitignoreCache{
		fsys:     fsys,
		patterns: make(map[string][]gitignore.Pattern),
	}
}

func (c *gitignoreCache) matches(path string, isDir bool) bool {
	dir := path
	if !isDir {
		dir = pathpkg.Dir(dir)
	}

	patterns := c.patternsFor(dir)
	if len(patterns) == 0 {
		return false
	}

	return gitignore.NewMatcher(patterns).Match(strings.Split(path, "/"), isDir)
}

func (c *gitignoreCache) patternsFor(dir string) []gitignore.Pattern {
	if cached, ok := c.patterns[dir]; ok {
		return cached
	}

	var parentPatterns []gitignore.Pattern
	if dir == "." || dir == "/" {
		parentPatterns = nil
	} else {
		parentPatterns = c.patternsFor(pathpkg.Dir(dir))
	}

	local := loadGitignore(c.fsys, pathDomain(dir))
	if len(local) == 0 {
		c.patterns[dir] = parentPatterns
		return parentPatterns
	}

	combined := make([]gitignore.Pattern, 0, len(parentPatterns)+len(local))
	combined = append(combined, parentPatterns...)
	combined = append(combined, local...)
	c.patterns[dir] = combined
	return combined
}

func searchFileWithContext(fsys fs.FS, path, displayPath string, re *regexp.Regexp, beforeContext, afterContext, limit int, multiline bool, showLineNumbers bool) []string {
	rc, isBinary, err := openTextFile(fsys, path)
	if err != nil || isBinary {
		return nil
	}
	defer rc.Close()

	var lines []string
	scanner := bufio.NewScanner(rc)

	buf := make([]byte, 0, DefaultScanBufSize)
	scanner.Buffer(buf, MaxScanBufSize)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	var scanCutoff string
	if err := scanner.Err(); err != nil {
		if !errors.Is(err, bufio.ErrTooLong) {

			return nil
		}
		scanCutoff = formatGrepLine(displayPath, len(lines)+1, true, fmt.Sprintf("line exceeds %dKB scan limit; remainder of file skipped", MaxScanBufSize/1024), showLineNumbers)
	}

	var results []string
	matchedLines := make([]bool, len(lines))
	anyMatch := false

	if multiline {

		full := strings.Join(lines, "\n")

		lineStarts := make([]int, len(lines))
		offset := 0
		for i, line := range lines {
			lineStarts[i] = offset
			offset += len(line) + 1
		}

		for _, m := range re.FindAllStringIndex(full, -1) {
			start, end := m[0], m[1]
			startLine := lineContaining(lineStarts, start)

			endProbe := end
			if endProbe > start {
				endProbe = end - 1
			}
			endLine := max(startLine, lineContaining(lineStarts, endProbe))
			for i := startLine; i <= endLine; i++ {
				matchedLines[i] = true
				anyMatch = true
			}
		}
	} else {
		for i, line := range lines {
			if re.MatchString(line) {
				matchedLines[i] = true
				anyMatch = true
			}
		}
	}

	if !anyMatch {
		return nil
	}

	printed := make([]bool, len(lines))
	lastPrinted := -2

	for i := range lines {
		if !matchedLines[i] {
			continue
		}

		if len(results) >= limit {
			break
		}

		start := max(0, i-beforeContext)
		end := min(len(lines)-1, i+afterContext)

		if lastPrinted >= 0 && start > lastPrinted+1 {
			results = append(results, "--")
		}

		for j := start; j <= end; j++ {
			if printed[j] {
				continue
			}
			printed[j] = true

			lineContent := lines[j]

			if len(lineContent) > MaxLineDisplayLength {
				lineContent = lineContent[:MaxLineDisplayLength-3] + "..."
			}

			results = append(results, formatGrepLine(displayPath, j+1, matchedLines[j], lineContent, showLineNumbers))
			lastPrinted = j
		}
	}

	if scanCutoff != "" && len(results) < limit {
		results = append(results, scanCutoff)
	}

	return results
}

func formatGrepLine(path string, lineNumber int, matched bool, content string, showLineNumbers bool) string {
	separator := "-"
	if matched {
		separator = ":"
	}
	if showLineNumbers {
		return fmt.Sprintf("%s%s%d%s%s", path, separator, lineNumber, separator, content)
	}
	return fmt.Sprintf("%s%s%s", path, separator, content)
}
