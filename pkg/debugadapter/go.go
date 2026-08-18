package debugadapter

import (
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

type goAdapter struct{}

func (goAdapter) Language() string { return "Go" }

func (goAdapter) Descriptor() dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name:             "delve",
		Language:         "Go",
		AdapterID:        "go",
		Command:          "dlv",
		Args:             []string{"dap", "--listen=127.0.0.1:0"},
		Transport:        dap.TransportTCP,
		ReadyPrefix:      "DAP server listening at:",
		TerminalStrategy: dap.TerminalAdapterProcess,
		Markers:          []string{"go.mod", "go.work"},
		SourceExtensions: []string{".go"},
		Defaults:         map[string]any{"type": "go"},
		ConfigurationPaths: []dap.ConfigurationPath{
			{Key: "program"},
			{Key: "cwd", Directory: true},
			{Key: "dlvCwd", Directory: true},
			{Key: "coreFilePath"},
			{Key: "traceDirPath", Directory: true},
		},
	}
}

func (goAdapter) Matches(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".go")
}

func (goAdapter) Detect(path string, source []byte) ([]Target, error) {
	files := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(files, path, source, parser.SkipObjectResolution|parser.ParseComments|parser.AllErrors)
	if parsed == nil {
		return nil, nil
	}

	isTestFile := strings.HasSuffix(strings.ToLower(path), "_test.go")
	testingPackages := goTestingPackages(parsed)
	examples := runnableGoExamples(parsed)
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
	if directory == "" {
		directory = "."
	}
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
		case parsed.Name.Name == "main" && name == "main" && validGoMain(function):
			kind = "main"
			detail = "main package"
		case isTestFile && goTestName(name, "Test") && validGoTest(function, "T", testingPackages):
			kind = "test"
			detail = "Go test"
		case isTestFile && goTestName(name, "Benchmark") && validGoTest(function, "B", testingPackages):
			kind = "benchmark"
			detail = "Go benchmark"
		case isTestFile && goTestName(name, "Fuzz") && validGoTest(function, "F", testingPackages):
			kind = "fuzz"
			detail = "Go fuzz test"
		case isTestFile && examples[name]:
			kind = "example"
			detail = "Go example"
		default:
			continue
		}
		position := files.Position(function.Name.Pos())
		targets = append(targets, Target{
			ID: fmt.Sprintf("go:%s:%s:%s", filepath.ToSlash(path), kind, name), Name: name,
			Detail: detail, Kind: kind, Language: "Go", Path: filepath.ToSlash(path),
			Directory: directory, Line: position.Line, Column: position.Column,
		})
	}
	if len(targets) == 0 && parseErr != nil {
		return nil, nil
	}
	return targets, nil
}

func goTestingPackages(file *ast.File) map[string]bool {
	packages := make(map[string]bool)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "testing" {
			continue
		}
		name := "testing"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name != "_" && name != "." {
			packages[name] = true
		}
	}
	return packages
}

func runnableGoExamples(file *ast.File) map[string]bool {
	result := make(map[string]bool)
	for _, example := range doc.Examples(file) {
		if example.Output == "" && !example.EmptyOutput {
			continue
		}
		result["Example"+example.Name] = true
	}
	return result
}

func validGoMain(function *ast.FuncDecl) bool {
	return emptyGoFieldList(function.Type.TypeParams) &&
		emptyGoFieldList(function.Type.Params) &&
		emptyGoFieldList(function.Type.Results)
}

func validGoTest(function *ast.FuncDecl, parameter string, testingPackages map[string]bool) bool {
	if !emptyGoFieldList(function.Type.TypeParams) || !emptyGoFieldList(function.Type.Results) || function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return false
	}
	field := function.Type.Params.List[0]
	if len(field.Names) > 1 {
		return false
	}
	pointer, ok := field.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != parameter {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && testingPackages[packageName.Name]
}

func emptyGoFieldList(fields *ast.FieldList) bool {
	return fields == nil || len(fields.List) == 0
}

func (goAdapter) Plan(request Request) (Plan, error) {
	program, err := projectPath(request.ProjectDir, request.Target.Directory)
	if err != nil {
		return Plan{}, err
	}
	configuration := map[string]any{"mode": "debug", "program": program}
	switch request.Target.Kind {
	case "main":
	case "test", "fuzz", "example":
		configuration["mode"] = "test"
		configuration["args"] = []string{"-test.run", "^" + regexp.QuoteMeta(request.Target.Name) + "$"}
	case "benchmark":
		configuration["mode"] = "test"
		configuration["args"] = []string{"-test.run", "^$", "-test.bench", "^" + regexp.QuoteMeta(request.Target.Name) + "$"}
	default:
		return Plan{}, fmt.Errorf("unsupported Go debug target kind %q", request.Target.Kind)
	}

	plan := Plan{
		Title:      actionLabel(request.Action) + " " + request.Target.Name,
		Summary:    fmt.Sprintf("%s the Go %s %s.", actionLabel(request.Action), request.Target.Kind, request.Target.Name),
		ProjectDir: request.ProjectDir, Request: "launch", Console: "internalConsole", Configuration: configuration,
	}
	if request.Action == "run" {
		configuration["noDebug"] = true
	} else {
		plan.Breakpoints = targetBreakpoint(request.Target)
	}
	return plan, nil
}

func goTestName(name, prefix string) bool {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || rest == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(rest)
	return first == utf8.RuneError || !unicode.IsLower(first)
}
