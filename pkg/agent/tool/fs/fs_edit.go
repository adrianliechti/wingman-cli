package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func EditTool(root *os.Root, allowedWriteRoots ...string) tool.Tool {
	return batchEditTool(root, nil, allowedWriteRoots...)
}

type editOp struct {
	oldText    string
	newText    string
	replaceAll bool
}

func newEditOp(entry map[string]any) (editOp, error) {
	oldText, ok := entry["old_string"].(string)
	if !ok {
		return editOp{}, fmt.Errorf("old_string is required")
	}

	newText, ok := entry["new_string"].(string)
	if !ok {
		return editOp{}, fmt.Errorf("new_string is required")
	}

	if oldText == newText && oldText != "" {
		return editOp{}, fmt.Errorf("old_string and new_string are identical")
	}

	replaceAll, _ := entry["replace_all"].(bool)

	return editOp{
		oldText:    normalizeToLF(oldText),
		newText:    normalizeToLF(newText),
		replaceAll: replaceAll,
	}, nil
}

func applyEditOp(content string, op editOp, pathArg string) (string, error) {
	if op.oldText == "" {
		if strings.TrimSpace(content) != "" {
			return "", fmt.Errorf("cannot create or replace empty file %s: file already has content", pathArg)
		}
		return op.newText, nil
	}

	actualOldText := findActualEditString(content, op.oldText)
	actualNewText := preserveEditQuoteStyle(op.oldText, actualOldText, op.newText)

	matchResult := fuzzyFindText(content, actualOldText)

	if !matchResult.found {
		preview := content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return "", fmt.Errorf("could not find old_string in %s. Make sure it matches exactly (including whitespace and newlines). File starts with:\n%s", pathArg, preview)
	}

	occurrences := strings.Count(content, actualOldText)
	if matchResult.usedFuzzyMatch {
		occurrences = strings.Count(normalizeForFuzzyMatch(content), normalizeForFuzzyMatch(actualOldText))
	}

	if occurrences > 1 && !op.replaceAll {
		return "", fmt.Errorf("found %d occurrences of the text in %s. The text must be unique — provide more context to make it unique, or set replace_all=true to replace all occurrences", occurrences, pathArg)
	}

	var newContent string

	if op.replaceAll {
		if matchResult.usedFuzzyMatch {
			if strings.Contains(normalizeForFuzzyMatch(actualNewText), normalizeForFuzzyMatch(actualOldText)) {
				return "", fmt.Errorf("replace_all made no progress in %s. Use an exact old_string or a replacement that changes the matched text", pathArg)
			}
			newContent = content
			for {
				mr := fuzzyFindText(newContent, actualOldText)
				if !mr.found {
					break
				}
				next := newContent[:mr.index] + actualNewText + newContent[mr.index+mr.matchLength:]
				if next == newContent {
					return "", fmt.Errorf("replace_all made no progress in %s. Use an exact old_string or a replacement that changes the matched text", pathArg)
				}
				newContent = next
			}
		} else {
			newContent = strings.ReplaceAll(content, actualOldText, actualNewText)
		}
	} else {
		newContent = content[:matchResult.index] + actualNewText + content[matchResult.index+matchResult.matchLength:]
	}

	if content == newContent {
		return "", fmt.Errorf("no changes made to %s. The replacement produced identical content", pathArg)
	}

	return newContent, nil
}

func writeRootFile(root *os.Root, path, content string) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := root.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	return root.WriteFile(path, []byte(content), 0666)
}

func findActualEditString(content, oldText string) string {
	if strings.Contains(content, oldText) {
		return oldText
	}

	normContent, offsets := normalizeEditQuotesWithMap(content)
	normOld := normalizeEditQuotes(oldText)

	idx := strings.Index(normContent, normOld)
	if idx == -1 {
		return oldText
	}

	return content[offsets[idx]:offsets[idx+len(normOld)]]
}

var editQuoteReplacer = strings.NewReplacer(
	"‘", "'", "’", "'",
	"“", "\"", "”", "\"",
)

func normalizeEditQuotes(text string) string {
	return editQuoteReplacer.Replace(text)
}

func normalizeEditQuotesWithMap(text string) (string, []int) {
	var b strings.Builder
	b.Grow(len(text))
	offsets := make([]int, 0, len(text)+1)
	offsets = append(offsets, 0)

	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch r {
		case '‘', '’':
			b.WriteByte('\'')
			offsets = append(offsets, i+size)
		case '“', '”':
			b.WriteByte('"')
			offsets = append(offsets, i+size)
		default:
			b.WriteString(text[i : i+size])
			for j := 1; j <= size; j++ {
				offsets = append(offsets, i+j)
			}
		}
		i += size
	}
	return b.String(), offsets
}

func preserveEditQuoteStyle(oldText, actualOldText, newText string) string {
	if oldText == actualOldText {
		return newText
	}

	result := newText
	if strings.ContainsAny(actualOldText, "“”") {
		result = applyCurlyDoubleQuotes(result)
	}
	if strings.ContainsAny(actualOldText, "‘’") {
		result = applyCurlySingleQuotes(result)
	}
	return result
}

func applyCurlyDoubleQuotes(text string) string {
	var b strings.Builder
	for i, r := range text {
		if r != '"' {
			b.WriteRune(r)
			continue
		}
		if isOpeningQuoteContext(text, i) {
			b.WriteString("“")
		} else {
			b.WriteString("”")
		}
	}
	return b.String()
}

func applyCurlySingleQuotes(text string) string {
	runes := []rune(text)
	var b strings.Builder
	for i, r := range runes {
		if r != '\'' {
			b.WriteRune(r)
			continue
		}

		if i > 0 && i < len(runes)-1 && isLetter(runes[i-1]) && isLetter(runes[i+1]) {
			b.WriteString("’")
			continue
		}

		if isOpeningQuoteContextRunes(runes, i) {
			b.WriteString("‘")
		} else {
			b.WriteString("’")
		}
	}
	return b.String()
}

func isOpeningQuoteContext(text string, byteIndex int) bool {
	preceding := []rune(text[:byteIndex])
	return isOpeningQuoteContextRunes(preceding, len(preceding))
}

func isOpeningQuoteContextRunes(runes []rune, index int) bool {
	if index == 0 {
		return true
	}
	prev := runes[index-1]
	switch prev {
	case ' ', '\t', '\n', '\r', '(', '[', '{':
		return true
	default:
		return false
	}
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
