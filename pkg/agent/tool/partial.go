package tool

import "strings"

func closeJSONPrefix(s string) string {
	var stack []byte
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 {
				return ""
			}
			open := stack[len(stack)-1]
			if c == '}' && open != '{' || c == ']' && open != '[' {
				return ""
			}
			stack = stack[:len(stack)-1]
		}
	}

	if !inString && len(stack) == 0 {
		return ""
	}

	out := s
	if escaped {
		out = out[:len(out)-1]
	}
	if inString {
		out += `"`
	}

	out = strings.TrimRight(out, " \t\r\n")
	if strings.HasSuffix(out, ":") {
		out += "null"
	} else {
		out = strings.TrimSuffix(out, ",")
	}

	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			out += "}"
		} else {
			out += "]"
		}
	}

	return out
}
