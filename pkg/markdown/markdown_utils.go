package markdown

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

func visibleLen(s string) int {
	runes := []rune(s)
	count := 0
	for i := 0; i < len(runes); {
		if runes[i] == '[' {
			// Escaped literal '[' => "[[]"
			if i+2 < len(runes) && runes[i+1] == '[' && runes[i+2] == ']' {
				count += runewidth.RuneWidth('[')
				i += 3
				continue
			}
			// Escaped literal ']' => "[]]"
			if i+2 < len(runes) && runes[i+1] == ']' && runes[i+2] == ']' {
				count += runewidth.RuneWidth(']')
				i += 3
				continue
			}
			// Tview tag => "[... ]" with no nested '['
			j := i + 1
			for j < len(runes) && runes[j] != ']' && runes[j] != '[' {
				j++
			}
			if j < len(runes) && runes[j] == ']' {
				i = j + 1
				continue
			}
		}

		count += runewidth.RuneWidth(runes[i])
		i++
	}

	return count
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

	runes := []rune(line)
	i := 0

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

	for i < len(runes) {
		r := runes[i]

		if r == '[' {
			// Escaped literal '[' => "[[]"
			if i+2 < len(runes) && runes[i+1] == '[' && runes[i+2] == ']' {
				currentWord.WriteString("[[]")
				currentWordLen += runewidth.RuneWidth('[')
				i += 3
				continue
			}
			// Escaped literal ']' => "[]]"
			if i+2 < len(runes) && runes[i+1] == ']' && runes[i+2] == ']' {
				currentWord.WriteString("[]]")
				currentWordLen += runewidth.RuneWidth(']')
				i += 3
				continue
			}
			// Tview tag => "[... ]" with no nested '['
			j := i + 1
			for j < len(runes) && runes[j] != ']' && runes[j] != '[' {
				j++
			}
			if j < len(runes) && runes[j] == ']' {
				tag := string(runes[i : j+1])
				currentWord.WriteString(tag)
				i = j + 1
				continue
			}
		}

		if r == ' ' {
			currentWord.WriteRune(r)
			currentWordLen += runewidth.RuneWidth(r)
			flushWord()
		} else {
			// Hard-wrap oversized words that exceed the width
			if currentWordLen >= width {
				flushWord()
			}
			currentWord.WriteRune(r)
			currentWordLen += runewidth.RuneWidth(r)
		}
		i++
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
