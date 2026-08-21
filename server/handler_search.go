package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const (
	defaultWorkspaceSearchLimit = 20000
	maxWorkspaceSearchLimit     = 20000
	maxWorkspaceSearchFileSize  = 5 << 20
	searchPreviewRunes          = 80
)

type workspaceSearchRequest struct {
	Query         string `json:"query"`
	Replacement   string `json:"replacement"`
	Regex         bool   `json:"regex"`
	CaseSensitive bool   `json:"case_sensitive"`
	WholeWord     bool   `json:"whole_word"`
	Include       string `json:"include"`
	Exclude       string `json:"exclude"`
	Limit         int    `json:"limit"`
}

type workspaceSearchMatch struct {
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	EndColumn   int    `json:"end_column"`
	Before      string `json:"before"`
	Text        string `json:"text"`
	After       string `json:"after"`
	Replacement string `json:"replacement"`
}

type workspaceSearchFile struct {
	Path     string                 `json:"path"`
	Revision string                 `json:"revision"`
	Matches  []workspaceSearchMatch `json:"matches"`
}

type workspaceSearchEvent struct {
	Type      string               `json:"type"`
	File      *workspaceSearchFile `json:"file,omitempty"`
	Files     int                  `json:"files,omitempty"`
	Matches   int                  `json:"matches,omitempty"`
	Truncated bool                 `json:"truncated,omitempty"`
}

type workspaceSearchConfig struct {
	request workspaceSearchRequest
	pattern *regexp.Regexp
	include []string
	exclude []string
	limit   int
}

func (s *Server) handleWorkspaceSearch(w http.ResponseWriter, r *http.Request) {
	var request workspaceSearchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	config, err := newWorkspaceSearchConfig(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	fys := s.workspace.Root.FS()
	ignore := newWorkspaceSearchIgnore(fys)
	files, matches := 0, 0
	truncated := false

	err = fs.WalkDir(fys, ".", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := r.Context().Err(); err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if entry.IsDir() {
			if filePath == "." {
				return nil
			}
			if workspaceSearchExcludedDir(entry.Name()) || ignore.matches(filePath, true) {
				return fs.SkipDir
			}
			return nil
		}
		if ignore.matches(filePath, false) || !config.matchesPath(filePath) {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr != nil || info.Size() > maxWorkspaceSearchFileSize {
			return nil
		}

		content, readErr := s.workspace.Root.ReadFile(filepath.FromSlash(filePath))
		if readErr != nil || isBinary(content) || !utf8.Valid(content) {
			return nil
		}
		remaining := config.limit - matches
		fileMatches, more := config.searchFile(string(content), remaining)
		if len(fileMatches) == 0 {
			return nil
		}
		files++
		matches += len(fileMatches)
		if more || matches >= config.limit {
			truncated = true
		}
		if err := encoder.Encode(workspaceSearchEvent{
			Type: "file",
			File: &workspaceSearchFile{
				Path:     filepath.ToSlash(filePath),
				Revision: fileRevision(content),
				Matches:  fileMatches,
			},
		}); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		if matches >= config.limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return
	}
	_ = encoder.Encode(workspaceSearchEvent{
		Type:      "done",
		Files:     files,
		Matches:   matches,
		Truncated: truncated,
	})
}

func newWorkspaceSearchConfig(request workspaceSearchRequest) (*workspaceSearchConfig, error) {
	if request.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	include := splitWorkspaceSearchGlobs(request.Include)
	exclude := splitWorkspaceSearchGlobs(request.Exclude)
	for _, pattern := range append(append([]string{}, include...), exclude...) {
		if _, err := doublestar.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
	}

	pattern := request.Query
	if !request.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !request.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %w", err)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = defaultWorkspaceSearchLimit
	}
	limit = min(limit, maxWorkspaceSearchLimit)
	return &workspaceSearchConfig{
		request: request,
		pattern: compiled,
		include: include,
		exclude: exclude,
		limit:   limit,
	}, nil
}

func (c *workspaceSearchConfig) matchesPath(filePath string) bool {
	filePath = filepath.ToSlash(filePath)
	if len(c.include) > 0 && !matchesWorkspaceSearchGlob(c.include, filePath) {
		return false
	}
	return !matchesWorkspaceSearchGlob(c.exclude, filePath)
}

func (c *workspaceSearchConfig) searchFile(content string, limit int) ([]workspaceSearchMatch, bool) {
	if limit <= 0 {
		return nil, true
	}
	lines := strings.Split(content, "\n")
	result := make([]workspaceSearchMatch, 0)
	for lineIndex, line := range lines {
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
		}
		indexes := c.pattern.FindAllStringSubmatchIndex(line, -1)
		for _, groups := range indexes {
			start, end := groups[0], groups[1]
			if c.request.WholeWord && !workspaceSearchWholeWord(line, start, end) {
				continue
			}
			if len(result) >= limit {
				return result, true
			}
			replacement := c.request.Replacement
			if c.request.Regex {
				replacement = string(c.pattern.ExpandString(nil, replacement, line, groups))
			}
			result = append(result, workspaceSearchMatch{
				Line:        lineIndex + 1,
				Column:      workspaceSearchUTF16Length(line[:start]) + 1,
				EndColumn:   workspaceSearchUTF16Length(line[:end]) + 1,
				Before:      workspaceSearchTail(line[:start], searchPreviewRunes),
				Text:        workspaceSearchHead(line[start:end], searchPreviewRunes*2),
				After:       workspaceSearchHead(line[end:], searchPreviewRunes),
				Replacement: replacement,
			})
		}
	}
	return result, false
}

func workspaceSearchExcludedDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn":
		return true
	default:
		return excludedDirs[name]
	}
}

func splitWorkspaceSearchGlobs(value string) []string {
	var patterns []string
	for field := range strings.FieldsSeq(value) {
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

func matchesWorkspaceSearchGlob(patterns []string, filePath string) bool {
	for _, pattern := range patterns {
		if matched, _ := doublestar.Match(pattern, path.Base(filePath)); matched {
			return true
		}
		if matched, _ := doublestar.Match(pattern, filePath); matched {
			return true
		}
	}
	return false
}

func workspaceSearchWholeWord(line string, start, end int) bool {
	if start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(line[:start])
		if workspaceSearchWordRune(previous) {
			return false
		}
	}
	if end < len(line) {
		next, _ := utf8.DecodeRuneInString(line[end:])
		if workspaceSearchWordRune(next) {
			return false
		}
	}
	return true
}

func workspaceSearchWordRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func workspaceSearchUTF16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func workspaceSearchHead(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func workspaceSearchTail(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return "…" + string(runes[len(runes)-limit:])
}

type workspaceSearchIgnore struct {
	fsys     fs.FS
	patterns map[string][]gitignore.Pattern
}

func newWorkspaceSearchIgnore(fsys fs.FS) *workspaceSearchIgnore {
	return &workspaceSearchIgnore{fsys: fsys, patterns: map[string][]gitignore.Pattern{}}
}

func (c *workspaceSearchIgnore) matches(filePath string, directory bool) bool {
	dir := filePath
	if !directory {
		dir = path.Dir(filePath)
	}
	patterns := c.patternsFor(dir)
	return len(patterns) > 0 && gitignore.NewMatcher(patterns).Match(strings.Split(filePath, "/"), directory)
}

func (c *workspaceSearchIgnore) patternsFor(dir string) []gitignore.Pattern {
	if patterns, ok := c.patterns[dir]; ok {
		return patterns
	}
	var parent []gitignore.Pattern
	if dir != "." && dir != "/" {
		parent = c.patternsFor(path.Dir(dir))
	}
	local := loadWorkspaceSearchIgnore(c.fsys, workspaceSearchDomain(dir))
	if len(local) == 0 {
		c.patterns[dir] = parent
		return parent
	}
	patterns := append(append([]gitignore.Pattern{}, parent...), local...)
	c.patterns[dir] = patterns
	return patterns
}

func loadWorkspaceSearchIgnore(fsys fs.FS, domain []string) []gitignore.Pattern {
	ignorePath := ".gitignore"
	if len(domain) > 0 {
		ignorePath = path.Join(append(append([]string{}, domain...), ".gitignore")...)
	}
	file, err := fsys.Open(ignorePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(file)
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

func workspaceSearchDomain(dir string) []string {
	if dir == "." || dir == "/" || dir == "" {
		return nil
	}
	return strings.Split(filepath.ToSlash(dir), "/")
}
