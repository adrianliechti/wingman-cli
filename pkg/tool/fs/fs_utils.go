package fs

import (
	"bufio"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 30 * 1024 // 30KB
)

// normalizePath converts an absolute path to a relative path if it starts with the working directory.
// This is needed because os.Root expects relative paths, but the LLM may provide absolute paths.
// Always returns paths with OS-native separators (backslash on Windows, forward slash on Unix).
func normalizePath(path, workingDir string) string {
	if !filepath.IsAbs(path) {
		return filepath.FromSlash(path)
	}

	absPath := cleanPath(path)
	absWorkingDir := cleanPath(workingDir)

	rel, err := filepath.Rel(absWorkingDir, absPath)

	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}

	return filepath.FromSlash(path)
}

// normalizePathFS normalizes a path and converts to forward slashes for fs.FS operations.
func normalizePathFS(path, workingDir string) string {
	return pathpkg.Clean(filepath.ToSlash(normalizePath(path, workingDir)))
}

// isOutsideWorkspace checks if an absolute path is outside the workspace.
// Returns true if the path is absolute and doesn't start with workingDir.
func isOutsideWorkspace(path, workingDir string) bool {
	if !filepath.IsAbs(path) {
		return false
	}

	absPath := cleanPath(path)
	absWorkingDir := cleanPath(workingDir)

	rel, err := filepath.Rel(absWorkingDir, absPath)

	if err != nil {
		return true
	}

	if rel == "." {
		return false
	}

	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cleanPath(path string) string {
	if path == "" {
		return path
	}

	return filepath.Clean(filepath.FromSlash(path))
}

// pathError creates a helpful error message for path-related issues.
func pathError(action, originalPath, normalizedPath, workingDir string, err error) error {
	if isOutsideWorkspace(originalPath, workingDir) {
		return fmt.Errorf("%s failed: path %q is outside workspace %q", action, originalPath, workingDir)
	}

	if originalPath != normalizedPath {
		return fmt.Errorf("%s failed: %s (resolved from %s): %w", action, normalizedPath, originalPath, err)
	}

	return fmt.Errorf("%s failed: %s: %w", action, originalPath, err)
}

func truncateHead(content string) (result string, byLines bool, byBytes bool) {
	lines := strings.Split(content, "\n")

	if len(lines) > DefaultMaxLines {
		lines = lines[:DefaultMaxLines]
		byLines = true
	}

	result = strings.Join(lines, "\n")

	if len(result) > DefaultMaxBytes {
		result = result[:DefaultMaxBytes]
		byBytes = true
	}

	return result, byLines, byBytes
}

func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")

	if lfIdx == -1 {
		return "\n"
	}

	if crlfIdx == -1 {
		return "\n"
	}

	if crlfIdx < lfIdx {
		return "\r\n"
	}

	return "\n"
}

func normalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	return text
}

func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}

	return text
}

func stripBom(content string) (bom string, text string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", content[len("\uFEFF"):]
	}

	return "", content
}

func mapFuzzyIndexToOriginal(original, fuzzy string, fuzzyIdx int) int {
	if fuzzyIdx <= 0 {
		return 0
	}

	if fuzzyIdx >= len(fuzzy) {
		return len(original)
	}

	fuzzyPos := 0
	originalPos := 0

	for originalPos < len(original) && fuzzyPos < fuzzyIdx {
		_, origSize := utf8.DecodeRuneInString(original[originalPos:])
		_, fuzzySize := utf8.DecodeRuneInString(fuzzy[fuzzyPos:])

		originalPos += origSize
		fuzzyPos += fuzzySize
	}

	return originalPos
}

func normalizeForFuzzyMatch(text string) string {
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	text = strings.Join(lines, "\n")

	replacements := map[string]string{
		// Smart single quotes
		"\u2018": "'", "\u2019": "'", "\u201A": "'", "\u201B": "'",
		// Smart double quotes
		"\u201C": "\"", "\u201D": "\"", "\u201E": "\"", "\u201F": "\"",
		// Various dashes/hyphens
		"\u2010": "-", "\u2011": "-", "\u2012": "-", "\u2013": "-", "\u2014": "-", "\u2015": "-", "\u2212": "-",
		// Special spaces
		"\u00A0": " ", "\u2002": " ", "\u2003": " ", "\u2004": " ", "\u2005": " ",
		"\u2006": " ", "\u2007": " ", "\u2008": " ", "\u2009": " ", "\u200A": " ",
		"\u202F": " ", "\u205F": " ", "\u3000": " ",
	}

	for old, new := range replacements {
		text = strings.ReplaceAll(text, old, new)
	}

	return text
}

type fuzzyMatchResult struct {
	found                 bool
	index                 int
	matchLength           int
	usedFuzzyMatch        bool
	contentForReplacement string
}

func fuzzyFindText(content, oldText string) fuzzyMatchResult {
	exactIndex := strings.Index(content, oldText)

	if exactIndex != -1 {
		return fuzzyMatchResult{
			found:                 true,
			index:                 exactIndex,
			matchLength:           len(oldText),
			usedFuzzyMatch:        false,
			contentForReplacement: content,
		}
	}

	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)

	if fuzzyIndex == -1 {
		return fuzzyMatchResult{
			found:                 false,
			index:                 -1,
			matchLength:           0,
			usedFuzzyMatch:        false,
			contentForReplacement: content,
		}
	}

	originalIndex := mapFuzzyIndexToOriginal(content, fuzzyContent, fuzzyIndex)
	originalEndIndex := mapFuzzyIndexToOriginal(content, fuzzyContent, fuzzyIndex+len(fuzzyOldText))

	return fuzzyMatchResult{
		found:                 true,
		index:                 originalIndex,
		matchLength:           originalEndIndex - originalIndex,
		usedFuzzyMatch:        true,
		contentForReplacement: content,
	}
}

func generateDiffString(oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var output strings.Builder
	maxLen := len(oldLines)

	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	lineNumWidth := len(fmt.Sprintf("%d", maxLen))

	i, j := 0, 0

	for i < len(oldLines) || j < len(newLines) {
		if i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j] {
			i++
			j++
			continue
		}

		if i < len(oldLines) && (j >= len(newLines) || !containsLine(newLines[j:], oldLines[i])) {
			output.WriteString(fmt.Sprintf("-%*d %s\n", lineNumWidth, i+1, oldLines[i]))
			i++
		} else if j < len(newLines) {
			output.WriteString(fmt.Sprintf("+%*d %s\n", lineNumWidth, j+1, newLines[j]))
			j++
		}
	}

	return output.String()
}

func containsLine(lines []string, line string) bool {
	for _, l := range lines {
		if l == line {
			return true
		}
	}

	return false
}

// Common ignore directories that should be skipped during file traversal
var defaultIgnoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".svn":         true,
	"__pycache__":  true,
	".venv":        true,
	"vendor":       true,
}

// isBinaryFile checks if a file is likely binary based on its extension
func isBinaryFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	binaryExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".bin": true, ".dat": true, ".db": true, ".sqlite": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".bmp": true, ".ico": true, ".webp": true, ".svg": true,
		".pdf": true, ".doc": true, ".docx": true, ".xls": true,
		".xlsx": true, ".ppt": true, ".pptx": true,
		".zip": true, ".tar": true, ".gz": true, ".rar": true,
		".7z": true, ".bz2": true, ".xz": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true,
		".wav": true, ".flac": true, ".ogg": true, ".webm": true,
		".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
		".pyc": true, ".pyo": true, ".class": true, ".o": true, ".a": true,
	}

	return binaryExts[ext]
}

// relPathSlash returns the relative path from base to target using forward slashes
func relPathSlash(base, target string) string {
	rel, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(target))

	if err != nil {
		return target
	}

	return filepath.ToSlash(rel)
}

// pathDomain returns the path split into components for gitignore matching
func pathDomain(fsPath string) []string {
	if fsPath == "" || fsPath == "." {
		return nil
	}

	return strings.Split(fsPath, "/")
}

// loadGitignore loads gitignore patterns from a .gitignore file
func loadGitignore(fsys fs.FS, domain []string) []gitignore.Pattern {
	gitignorePath := ".gitignore"

	if len(domain) > 0 {
		gitignorePath = pathpkg.Join(append(domain, ".gitignore")...)
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