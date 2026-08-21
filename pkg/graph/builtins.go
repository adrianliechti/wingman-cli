package graph

import (
	"regexp"
	"strings"
	"sync"
)

// Builtin names are derived from the grammar's highlight query captures
// (@function.builtin / @constructor.builtin), not hand-maintained lists.
var builtinsByLang sync.Map

var builtinPredicate = regexp.MustCompile(
	`#(any-of|eq|match)\?\s+@(?:function|constructor)\.builtin[\w.]*((?:\s+"(?:[^"\\]|\\.)*")+)`,
)

var quotedLiteral = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

func builtinNames(lang, highlightQuery string) map[string]bool {
	if cached, ok := builtinsByLang.Load(lang); ok {
		return cached.(map[string]bool)
	}

	names := map[string]bool{}
	for _, m := range builtinPredicate.FindAllStringSubmatch(highlightQuery, -1) {
		for _, q := range quotedLiteral.FindAllStringSubmatch(m[2], -1) {
			switch m[1] {
			case "eq", "any-of":
				if identLike(q[1]) {
					names[q[1]] = true
				}
			case "match":
				for _, alt := range alternation(q[1]) {
					names[alt] = true
				}
			}
		}
	}

	builtinsByLang.Store(lang, names)
	return names
}

func alternation(pattern string) []string {
	inner, ok := strings.CutPrefix(pattern, "^(")
	if !ok {
		return nil
	}
	inner, ok = strings.CutSuffix(inner, ")$")
	if !ok {
		return nil
	}
	var out []string
	for _, alt := range strings.Split(inner, "|") {
		if identLike(alt) {
			out = append(out, alt)
		}
	}
	return out
}

func identLike(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i]) && s[i] != '$' {
			return false
		}
	}
	return true
}
