package fs

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/sergi/go-diff/diffmatchpatch"
)

const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 48 * 1024

	MaxReadFileBytes = 10 * 1024 * 1024
	MaxEditFileBytes = 10 * 1024 * 1024
)

func detectLineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
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

func normalizeForFuzzyMatch(text string) string {
	normalized, _ := normalizeForFuzzyMatchWithMap(text)
	return normalized
}

func normalizeForFuzzyMatchWithMap(text string) (string, []int) {
	var b strings.Builder
	offsetMap := []int{0}

	for lineStart := 0; lineStart <= len(text); {
		lineEnd := strings.IndexByte(text[lineStart:], '\n')
		hasNewline := lineEnd != -1
		if hasNewline {
			lineEnd += lineStart
		} else {
			lineEnd = len(text)
		}

		trimmedEnd := lineEnd
		for trimmedEnd > lineStart && (text[trimmedEnd-1] == ' ' || text[trimmedEnd-1] == '\t') {
			trimmedEnd--
		}

		for pos := lineStart; pos < trimmedEnd; {
			r, size := utf8.DecodeRuneInString(text[pos:trimmedEnd])
			replacement := normalizeFuzzyRune(r)
			b.WriteString(replacement)

			originalBytes := text[pos : pos+size]
			if replacement == originalBytes {
				for i := 1; i <= size; i++ {
					offsetMap = append(offsetMap, pos+i)
				}
			} else {
				for i := 0; i < len(replacement); i++ {
					offsetMap = append(offsetMap, pos+size)
				}
			}

			pos += size
		}

		if !hasNewline {
			break
		}

		b.WriteByte('\n')
		offsetMap = append(offsetMap, lineEnd+1)
		lineStart = lineEnd + 1
	}

	return b.String(), offsetMap
}

func normalizeFuzzyRune(r rune) string {
	switch r {
	case '\u2018', '\u2019', '\u201A', '\u201B':
		return "'"
	case '\u201C', '\u201D', '\u201E', '\u201F':
		return "\""
	case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
		return "-"
	case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008',
		'\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
		return " "
	default:
		return string(r)
	}
}

type fuzzyMatchResult struct {
	found          bool
	index          int
	matchLength    int
	usedFuzzyMatch bool
}

func fuzzyFindText(content, oldText string) fuzzyMatchResult {
	if i := strings.Index(content, oldText); i != -1 {
		return fuzzyMatchResult{found: true, index: i, matchLength: len(oldText)}
	}

	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	if fuzzyOldText == "" {
		return fuzzyMatchResult{index: -1}
	}

	fuzzyContent, fuzzyToOriginal := normalizeForFuzzyMatchWithMap(content)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return fuzzyMatchResult{index: -1}
	}

	originalIndex := fuzzyToOriginal[fuzzyIndex]
	originalEnd := fuzzyToOriginal[fuzzyIndex+len(fuzzyOldText)]

	return fuzzyMatchResult{
		found:          true,
		index:          originalIndex,
		matchLength:    originalEnd - originalIndex,
		usedFuzzyMatch: true,
	}
}

// The model just produced edited content, so echo only a bounded diff. Both
// dimensions matter: a line cap alone still permits a minified or encoded line
// to flood the context and UI.
const (
	maxDiffLines     = 200
	maxDiffLineBytes = 4 * 1024
)

func generateDiffString(oldContent, newContent string) string {
	dmp := diffmatchpatch.New()

	oldLines, newLines, lineArray := dmp.DiffLinesToChars(oldContent, newContent)
	diffs := dmp.DiffMain(oldLines, newLines, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	diffs = dmp.DiffCleanupSemantic(diffs)

	var output strings.Builder
	oldLineNum := 1
	newLineNum := 1

	for _, diff := range diffs {
		lines := strings.Split(diff.Text, "\n")

		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		switch diff.Type {
		case diffmatchpatch.DiffEqual:
			oldLineNum += len(lines)
			newLineNum += len(lines)
		case diffmatchpatch.DiffDelete:
			for _, line := range lines {
				fmt.Fprintf(&output, "-%d %s\n", oldLineNum, truncateDiffLine(line))
				oldLineNum++
			}
		case diffmatchpatch.DiffInsert:
			for _, line := range lines {
				fmt.Fprintf(&output, "+%d %s\n", newLineNum, truncateDiffLine(line))
				newLineNum++
			}
		}
	}

	return capDiffLines(output.String())
}

func truncateDiffLine(line string) string {
	if len(line) <= maxDiffLineBytes {
		return line
	}
	prefix := line[:maxDiffLineBytes]
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix + fmt.Sprintf("… [diff line truncated: %d bytes omitted]", len(line)-len(prefix))
}

func capDiffLines(diff string) string {
	trimmed := strings.TrimRight(diff, "\n")
	if trimmed == "" {
		return diff
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) <= maxDiffLines {
		return diff
	}

	omitted := len(lines) - maxDiffLines
	lines = lines[:maxDiffLines]

	return strings.Join(lines, "\n") + fmt.Sprintf("\n… diff truncated: %d more changed lines (the file was written in full)\n", omitted)
}

var vcsDirs = map[string]bool{
	".git": true,
	".svn": true,
	".hg":  true,
	".bzr": true,
	".jj":  true,
	".sl":  true,
}

// binarySniffLen bounds how many leading bytes are inspected when classifying
// a file as binary.
const binarySniffLen = 8000

// isBinaryContent reports whether data looks like binary (non-text) content.
// It uses the same NUL-byte heuristic as git and grep: a NUL within the first
// several KB reliably marks binary content, while text files — including UTF-8
// sources, SVG, JSON, and extension-less docs — never contain one.
func isBinaryContent(data []byte) bool {
	if len(data) > binarySniffLen {
		data = data[:binarySniffLen]
	}
	return bytes.IndexByte(data, 0) >= 0
}

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

func pathDomain(fsPath string) []string {
	if fsPath == "" || fsPath == "." {
		return nil
	}

	return strings.Split(fsPath, "/")
}

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
