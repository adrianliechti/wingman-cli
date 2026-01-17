package markdown

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var tagRe = regexp.MustCompile(`\[[^\]]*\]`)

func visibleLen(s string) int {
	clean := tagRe.ReplaceAllString(s, "")

	return utf8.RuneCountInString(clean)
}

func wrapLine(line string, width int) []string {
	if width <= 0 || visibleLen(line) <= width {
		return []string{line}
	}

	var result []string

	var currentLine strings.Builder
	var currentWord strings.Builder

	currentLineLen := 0
	currentWordLen := 0

	inTag := false
	var tagBuilder strings.Builder

	flushWord := func() {
		if currentWord.Len() == 0 {
			return
		}

		if currentLineLen+currentWordLen > width && currentLineLen > 0 {
			result = append(result, strings.TrimRight(currentLine.String(), " "))
			currentLine.Reset()
			currentLineLen = 0
		}

		currentLine.WriteString(currentWord.String())
		currentLineLen += currentWordLen
		currentWord.Reset()
		currentWordLen = 0
	}

	for _, r := range line {
		if r == '[' && !inTag {
			inTag = true
			tagBuilder.Reset()
			tagBuilder.WriteRune(r)
			continue
		}

		if inTag {
			tagBuilder.WriteRune(r)
			if r == ']' {
				inTag = false
				currentWord.WriteString(tagBuilder.String())
			}
			continue
		}

		if r == ' ' {
			currentWord.WriteRune(r)
			currentWordLen++
			flushWord()
		} else {
			currentWord.WriteRune(r)
			currentWordLen++
		}
	}

	flushWord()

	if currentLine.Len() > 0 {
		result = append(result, currentLine.String())
	}

	if len(result) == 0 {
		return []string{line}
	}

	return result
}
