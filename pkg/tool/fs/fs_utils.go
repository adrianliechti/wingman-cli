package fs

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 30 * 1024 // 30KB
)

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
