package debugadapter

import "bytes"

// maskCStyleSource removes comments and quoted literals while preserving byte
// offsets and newlines. The target detectors only need a small lexical view;
// they intentionally do not try to replace each language's parser.
func maskCStyleSource(source []byte, singleQuotes, backticks bool) []byte {
	masked := bytes.Clone(source)
	const (
		normal = iota
		lineComment
		blockComment
		singleQuoted
		doubleQuoted
		backtickQuoted
	)
	state := normal
	escaped := false
	for index := 0; index < len(masked); index++ {
		current := masked[index]
		next := byte(0)
		if index+1 < len(masked) {
			next = masked[index+1]
		}
		switch state {
		case normal:
			switch {
			case current == '/' && next == '/':
				masked[index], masked[index+1] = ' ', ' '
				index++
				state = lineComment
			case current == '/' && next == '*':
				masked[index], masked[index+1] = ' ', ' '
				index++
				state = blockComment
			case current == '"':
				masked[index] = ' '
				state = doubleQuoted
				escaped = false
			case current == '\'' && singleQuotes:
				masked[index] = ' '
				state = singleQuoted
				escaped = false
			case current == '`' && backticks:
				masked[index] = ' '
				state = backtickQuoted
				escaped = false
			}
		case lineComment:
			if current == '\n' || current == '\r' {
				state = normal
			} else {
				masked[index] = ' '
			}
		case blockComment:
			if current == '*' && next == '/' {
				masked[index], masked[index+1] = ' ', ' '
				index++
				state = normal
			} else if current != '\n' && current != '\r' {
				masked[index] = ' '
			}
		case singleQuoted, doubleQuoted, backtickQuoted:
			if current == '\n' || current == '\r' {
				// JavaScript templates and Java/C# multiline literals may span
				// lines, so preserve the state as well as the line structure.
				continue
			}
			masked[index] = ' '
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if (state == singleQuoted && current == '\'') ||
				(state == doubleQuoted && current == '"') ||
				(state == backtickQuoted && current == '`') {
				state = normal
			}
		}
	}
	return masked
}

func sourceLineColumn(source []byte, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	line := bytes.Count(source[:offset], []byte{'\n'}) + 1
	lastNewline := bytes.LastIndexByte(source[:offset], '\n')
	return line, offset - lastNewline
}
