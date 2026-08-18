package debugtarget

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type goDetector struct{}

func (goDetector) Matches(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".go")
}

func (goDetector) Detect(path string, source []byte) ([]Target, error) {
	files := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(files, path, source, parser.SkipObjectResolution|parser.AllErrors)
	if parsed == nil {
		// Draft editor content is frequently temporarily invalid. CodeLens
		// discovery should disappear quietly until the syntax is parseable.
		return nil, nil
	}

	isTestFile := strings.HasSuffix(strings.ToLower(path), "_test.go")
	var targets []Target
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Name == nil {
			continue
		}
		kind := ""
		detail := ""
		name := function.Name.Name
		switch {
		case parsed.Name.Name == "main" && name == "main":
			kind = "main"
			detail = "main package"
		case isTestFile && goTestName(name, "Test"):
			kind = "test"
			detail = "Go test"
		case isTestFile && goTestName(name, "Benchmark"):
			kind = "benchmark"
			detail = "Go benchmark"
		case isTestFile && goTestName(name, "Fuzz"):
			kind = "fuzz"
			detail = "Go fuzz test"
		case isTestFile && strings.HasPrefix(name, "Example"):
			kind = "example"
			detail = "Go example"
		default:
			continue
		}
		position := files.Position(function.Name.Pos())
		targets = append(targets, Target{
			ID:       fmt.Sprintf("go:%s:%d:%s", filepath.ToSlash(path), position.Line, name),
			Name:     name,
			Detail:   detail,
			Kind:     kind,
			Language: "Go",
			Path:     filepath.ToSlash(path),
			Line:     position.Line,
			Column:   position.Column,
		})
	}

	// A partial AST is still useful for stable declarations before the syntax
	// error, so only surface the parser error when no candidate could be found.
	if len(targets) == 0 && parseErr != nil {
		return nil, nil
	}
	return targets, nil
}

func goTestName(name, prefix string) bool {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || rest == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(rest)
	return first == utf8.RuneError || !unicode.IsLower(first)
}
