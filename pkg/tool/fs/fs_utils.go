package fs

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/sergi/go-diff/diffmatchpatch"
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

	if rel, ok := relPathWithinWorkspace(path, workingDir); ok {
		return rel
	}

	return filepath.FromSlash(path)
}

// normalizePathFS normalizes a path and converts to forward slashes for fs.FS operations.
func normalizePathFS(path, workingDir string) string {
	return pathpkg.Clean(filepath.ToSlash(normalizePath(path, workingDir)))
}

// ensurePathInWorkspace validates that a path is inside the workspace and returns a normalized path.
func ensurePathInWorkspace(pathArg, workingDir, action string) (string, error) {
	if isOutsideWorkspace(pathArg, workingDir) {
		return "", fmt.Errorf("cannot %s: path %q is outside workspace %q", action, pathArg, workingDir)
	}

	return normalizePath(pathArg, workingDir), nil
}

// ensurePathInWorkspaceFS validates that a path is inside the workspace and returns a normalized fs.FS path.
func ensurePathInWorkspaceFS(pathArg, workingDir, action string) (string, error) {
	if isOutsideWorkspace(pathArg, workingDir) {
		return "", fmt.Errorf("cannot %s: path %q is outside workspace %q", action, pathArg, workingDir)
	}

	return normalizePathFS(pathArg, workingDir), nil
}

// isOutsideWorkspace checks if an absolute path is outside the workspace.
// Returns true if the path is absolute and doesn't start with workingDir.
func isOutsideWorkspace(path, workingDir string) bool {
	if !filepath.IsAbs(path) {
		return false
	}

	_, ok := relPathWithinWorkspace(path, workingDir)

	return !ok
}

// relPathWithinWorkspace returns the relative path from workingDir to absPath
// if absPath is within workingDir. It preserves the original casing where possible.
func relPathWithinWorkspace(absPath, workingDir string) (string, bool) {
	if !filepath.IsAbs(absPath) {
		return filepath.FromSlash(absPath), true
	}

	absPathClean := cleanPath(absPath)
	absWorkingDir := cleanPath(workingDir)

	compPath := normalizePathForComparison(absPathClean)
	compWorking := normalizePathForComparison(absWorkingDir)
	sep := string(filepath.Separator)

	if compPath == compWorking {
		return ".", true
	}

	prefix := compWorking
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}

	if strings.HasPrefix(compPath, prefix) {
		if strings.HasSuffix(absWorkingDir, sep) {
			return absPathClean[len(absWorkingDir):], true
		}

		return absPathClean[len(absWorkingDir)+len(sep):], true
	}

	relComp, err := filepath.Rel(compWorking, compPath)

	if err != nil {
		return "", false
	}

	if relComp == "." {
		return ".", true
	}

	if relComp == ".." || strings.HasPrefix(relComp, ".."+sep) {
		return "", false
	}

	if relOrig, err := filepath.Rel(absWorkingDir, absPathClean); err == nil {
		if relOrig == "." {
			return ".", true
		}

		if relOrig != ".." && !strings.HasPrefix(relOrig, ".."+sep) {
			return relOrig, true
		}
	}

	return relComp, true
}

func cleanPath(path string) string {
	if path == "" {
		return path
	}

	return filepath.Clean(filepath.FromSlash(path))
}

// normalizePathForComparison normalizes paths for case-insensitive comparison.
// Windows paths are fully case-insensitive, and macOS (APFS) is case-insensitive by default.
// We treat both as case-insensitive for path comparison.
func normalizePathForComparison(path string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
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
	// Trim trailing whitespace from each line
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	text = strings.Join(lines, "\n")

	// Replace common Unicode variations that LLMs often introduce
	replacer := strings.NewReplacer(
		// Smart quotes → ASCII quotes
		"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'", // single
		"\u201C", "\"", "\u201D", "\"", "\u201E", "\"", "\u201F", "\"", // double
		// Dashes → hyphen
		"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-",
		"\u2014", "-", "\u2015", "-", "\u2212", "-",
		// Special spaces → regular space
		"\u00A0", " ", "\u2002", " ", "\u2003", " ", "\u2004", " ", "\u2005", " ",
		"\u2006", " ", "\u2007", " ", "\u2008", " ", "\u2009", " ", "\u200A", " ",
		"\u202F", " ", "\u205F", " ", "\u3000", " ",
	)

	return replacer.Replace(text)
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
	dmp := diffmatchpatch.New()

	// Create line-based diff for better readability
	oldLines, newLines, lineArray := dmp.DiffLinesToChars(oldContent, newContent)
	diffs := dmp.DiffMain(oldLines, newLines, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	diffs = dmp.DiffCleanupSemantic(diffs)

	var output strings.Builder
	oldLineNum := 1
	newLineNum := 1

	for _, diff := range diffs {
		lines := strings.Split(diff.Text, "\n")

		// Remove empty last element from split if text ends with newline
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		switch diff.Type {
		case diffmatchpatch.DiffEqual:
			oldLineNum += len(lines)
			newLineNum += len(lines)
		case diffmatchpatch.DiffDelete:
			for _, line := range lines {
				output.WriteString(fmt.Sprintf("-%d %s\n", oldLineNum, line))
				oldLineNum++
			}
		case diffmatchpatch.DiffInsert:
			for _, line := range lines {
				output.WriteString(fmt.Sprintf("+%d %s\n", newLineNum, line))
				newLineNum++
			}
		}
	}

	return output.String()
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

var binaryExtensions = map[string]bool{
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

// isBinaryFile checks if a file is likely binary based on its extension
func isBinaryFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))

	return binaryExtensions[ext]
}

// relPathSlash returns the relative path from base to target using forward slashes
func relPathSlash(base, target string) string {
	rel, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(target))

	if err != nil {
		return target
	}

	return filepath.ToSlash(rel)
}

func relPathFromBase(base, path string) string {
	if base == "." {
		return path
	}

	return relPathSlash(base, path)
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

// walkWorkspace traverses files under root, respecting gitignore and default ignore dirs.
// It skips symlinks and calls onFile for each non-ignored file.
// Returning filepath.SkipAll from onFile stops traversal.
func walkWorkspace(ctx context.Context, fsys fs.FS, root string, onFile func(path, relPath string) error) error {
	var allPatterns []gitignore.Pattern
	allPatterns = append(allPatterns, loadGitignore(fsys, nil)...)
	matcher := gitignore.NewMatcher(allPatterns)

	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Skip symlinks to prevent infinite loops
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		if d.IsDir() && defaultIgnoreDirs[d.Name()] {
			return filepath.SkipDir
		}

		relPath := relPathFromBase(root, path)
		pathParts := strings.Split(relPath, "/")

		if d.IsDir() {
			if matcher.Match(pathParts, true) {
				return filepath.SkipDir
			}

			newPatterns := loadGitignore(fsys, pathDomain(path))

			if len(newPatterns) > 0 {
				allPatterns = append(allPatterns, newPatterns...)
				matcher = gitignore.NewMatcher(allPatterns)
			}

			return nil
		}

		if matcher.Match(pathParts, false) {
			return nil
		}

		return onFile(path, relPath)
	})
}
