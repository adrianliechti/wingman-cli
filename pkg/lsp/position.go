package lsp

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return splitLines(string(data)), nil
}

func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

func utf16ColFromRune(line string, runeCol int) int {
	col := 0
	i := 0
	for _, r := range line {
		if i >= runeCol {
			break
		}
		if r > 0xFFFF {
			col += 2
		} else {
			col++
		}
		i++
	}
	return col
}

func runeColFromUTF16(line string, utf16Col int) int {
	col := 0
	runes := 0
	for _, r := range line {
		if col >= utf16Col {
			break
		}
		if r > 0xFFFF {
			col += 2
		} else {
			col++
		}
		runes++
	}
	return runes
}

func isWordChar(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func findWordInLine(line, word string) int {
	return findWordInLineFrom(line, word, 0)
}

func findWordInLineFrom(line, word string, fromRune int) int {
	if word == "" {
		return -1
	}

	runes := []rune(line)
	target := []rune(word)

	for i := max(fromRune, 0); i+len(target) <= len(runes); i++ {
		match := true
		for j := range target {
			if runes[i+j] != target[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if i > 0 && isWordChar(runes[i-1]) {
			continue
		}
		if end := i + len(target); end < len(runes) && isWordChar(runes[end]) {
			continue
		}
		return i
	}

	return -1
}

func PositionFromDisplay(path string, line, column int) (Position, error) {
	lines, err := readLines(path)
	if err != nil {
		return Position{}, err
	}

	if line > len(lines) {
		return Position{}, fmt.Errorf("line %d is out of range: file has %d lines", line, len(lines))
	}

	return Position{Line: line - 1, Character: utf16ColFromRune(lines[line-1], column-1)}, nil
}

func PositionOfSymbolOnLine(path string, line int, symbol string) (Position, error) {
	lines, err := readLines(path)
	if err != nil {
		return Position{}, err
	}

	if line > len(lines) {
		return Position{}, fmt.Errorf("line %d is out of range: file has %d lines", line, len(lines))
	}

	text := lines[line-1]
	runeCol := findWordInLine(text, symbol)
	if runeCol < 0 {
		return Position{}, fmt.Errorf("symbol %q not found on line %d", symbol, line)
	}

	return Position{Line: line - 1, Character: utf16ColFromRune(text, runeCol)}, nil
}

func PositionOfSymbol(path string, symbol string) (Position, bool) {
	lines, err := readLines(path)
	if err != nil {
		return Position{}, false
	}

	for i, text := range lines {
		if runeCol := findWordInLine(text, symbol); runeCol >= 0 {
			return Position{Line: i, Character: utf16ColFromRune(text, runeCol)}, true
		}
	}

	return Position{}, false
}
