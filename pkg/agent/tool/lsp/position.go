package lsp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/language"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

type symbolCandidate struct {
	name      string
	qualified string
	position  lsp.Position
}

func positionFromDisplay(path string, line, column int) (lsp.Position, error) {
	lines, err := readLines(path)
	if err != nil {
		return lsp.Position{}, err
	}
	if line < 1 || line > len(lines) {
		return lsp.Position{}, fmt.Errorf("line %d out of range (file has %d lines)", line, len(lines))
	}
	runes := []rune(lines[line-1])
	if column < 1 || column > len(runes)+1 {
		return lsp.Position{}, fmt.Errorf("column %d out of range for line %d (line has %d columns)", column, line, len(runes)+1)
	}
	return lsp.Position{Line: uint32(line - 1), Character: uint32(utf16ColumnFromRune(lines[line-1], column-1))}, nil
}

func positionOfSymbolOnLine(path string, line int, symbol string) (lsp.Position, error) {
	lines, err := readLines(path)
	if err != nil {
		return lsp.Position{}, err
	}
	if line < 1 || line > len(lines) {
		return lsp.Position{}, fmt.Errorf("line %d out of range (file has %d lines)", line, len(lines))
	}
	column := findWordInLine(lines[line-1], symbol)
	if column < 0 {
		return lsp.Position{}, fmt.Errorf("symbol %q not found on line %d", symbol, line)
	}
	return lsp.Position{Line: uint32(line - 1), Character: uint32(utf16ColumnFromRune(lines[line-1], column))}, nil
}

func symbolPosition(ctx context.Context, session *lsp.Session, uri, name string) (lsp.Position, bool) {
	path, _ := fileuri.Path(uri)
	result, err := session.DocumentSymbols(ctx, uri)
	documentSymbols, symbols := language.DocumentSymbolsFromProtocol(result)
	if err == nil {
		var candidates []symbolCandidate
		for _, symbol := range symbols {
			candidates = append(candidates, symbolCandidate{name: symbol.Name, qualified: symbol.Name, position: symbol.Location.Range.Start})
		}
		collectSymbolCandidates(documentSymbols, "", &candidates)
		if matched, ok := matchSymbol(candidates, name); ok {
			return snapToIdentifier(path, matched), true
		}
	}
	return positionOfSymbol(path, symbolLeaf(name))
}

func snapToIdentifier(path string, matched symbolCandidate) lsp.Position {
	position := matched.position
	lines, err := readLines(path)
	if err != nil || int(position.Line) >= len(lines) {
		return position
	}
	text := lines[position.Line]
	if index := findWordInLineFrom(text, symbolLeaf(matched.name), runeColumnFromUTF16(text, int(position.Character))); index >= 0 {
		position.Character = uint32(utf16ColumnFromRune(text, index))
	}
	return position
}

func collectSymbolCandidates(symbols []lsp.DocumentSymbol, prefix string, result *[]symbolCandidate) {
	for _, symbol := range symbols {
		qualified := symbol.Name
		if prefix != "" {
			qualified = prefix + "." + symbol.Name
		}
		*result = append(*result, symbolCandidate{name: symbol.Name, qualified: qualified, position: symbol.SelectionRange.Start})
		collectSymbolCandidates(symbol.Children, qualified, result)
	}
}

func matchSymbol(candidates []symbolCandidate, query string) (symbolCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.name == query || candidate.qualified == query {
			return candidate, true
		}
	}
	if strings.Contains(query, ".") {
		normalized := normalizeSymbol(query)
		for _, candidate := range candidates {
			if strings.HasSuffix(normalizeSymbol(candidate.qualified), normalized) || strings.HasSuffix(normalizeSymbol(candidate.name), normalized) {
				return candidate, true
			}
		}
	}
	leaf := symbolLeaf(query)
	for _, candidate := range candidates {
		if symbolLeaf(candidate.name) == leaf {
			return candidate, true
		}
	}
	return symbolCandidate{}, false
}

func positionOfSymbol(path, symbol string) (lsp.Position, bool) {
	lines, err := readLines(path)
	if err != nil {
		return lsp.Position{}, false
	}
	for line, text := range lines {
		if column := findWordInLine(text, symbol); column >= 0 {
			return lsp.Position{Line: uint32(line), Character: uint32(utf16ColumnFromRune(text, column))}, true
		}
	}
	return lsp.Position{}, false
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines, nil
}

func utf16ColumnFromRune(line string, runeColumn int) int {
	column := 0
	for index, value := range []rune(line) {
		if index >= runeColumn {
			break
		}
		if value > 0xffff {
			column += 2
		} else {
			column++
		}
	}
	return column
}

func runeColumnFromUTF16(line string, utf16Column int) int {
	column, runes := 0, 0
	for _, value := range line {
		if column >= utf16Column {
			break
		}
		if value > 0xffff {
			column += 2
		} else {
			column++
		}
		runes++
	}
	return runes
}

func findWordInLine(line, word string) int { return findWordInLineFrom(line, word, 0) }

func findWordInLineFrom(line, word string, start int) int {
	if word == "" {
		return -1
	}
	lineRunes, wordRunes := []rune(line), []rune(word)
	for index := max(start, 0); index+len(wordRunes) <= len(lineRunes); index++ {
		if string(lineRunes[index:index+len(wordRunes)]) != word {
			continue
		}
		if index > 0 && isWordCharacter(lineRunes[index-1]) || index+len(wordRunes) < len(lineRunes) && isWordCharacter(lineRunes[index+len(wordRunes)]) {
			continue
		}
		return index
	}
	return -1
}

func isWordCharacter(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func symbolLeaf(name string) string {
	name = strings.TrimSuffix(name, "()")
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	return strings.Trim(name, "*()")
}

func normalizeSymbol(name string) string {
	return strings.Map(func(value rune) rune {
		switch value {
		case '(', ')', '*', ' ':
			return -1
		default:
			return value
		}
	}, name)
}
