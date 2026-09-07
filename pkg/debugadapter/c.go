package debugadapter

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

var cMainPattern = regexp.MustCompile(`\b(?:int\s+main\s*\([^;{}]*\)|auto\s+main\s*\([^;{}]*\)\s*->\s*int)\s*\{`)

const cLanguage = "C/C++"

type cAdapter struct{}

func (cAdapter) Language() string { return cLanguage }

func (cAdapter) Descriptor() dap.AdapterDescriptor {
	descriptor := lldbDescriptor("codelldb-native", cLanguage)
	descriptor.Markers = []string{"compile_flags.txt", "compile_commands.json", ".clangd", "CMakeLists.txt", "meson.build", "Makefile"}
	descriptor.SourceExtensions = []string{".c", ".cpp", ".cc", ".cxx", ".c++"}
	return descriptor
}

func (cAdapter) Matches(path string) bool {
	return nativeSourceLanguage(path) != ""
}

func nativeSourceLanguage(path string) string {
	extension := filepath.Ext(path)
	if extension == ".c" {
		return "C"
	}
	// Compilers treat uppercase .C as C++.
	if extension == ".C" {
		return "C++"
	}
	switch strings.ToLower(extension) {
	case ".cpp", ".cc", ".cxx", ".c++":
		return "C++"
	}
	return ""
}

func (cAdapter) Detect(path string, source []byte) ([]Target, error) {
	masked := maskCStyleSource(source, true, false)
	match := cMainPattern.FindIndex(masked)
	if match == nil {
		return nil, nil
	}
	offset := match[0] + bytes.Index(masked[match[0]:match[1]], []byte("main"))
	line, column := sourceLineColumn(source, offset)
	return []Target{{
		ID: "native:" + filepath.ToSlash(path) + ":main", Name: "main", Kind: "main", Language: cLanguage,
		Detail: nativeSourceLanguage(path) + " entry point", Path: filepath.ToSlash(path),
		Directory: filepath.ToSlash(filepath.Dir(path)), Line: line, Column: column,
	}}, nil
}

func (adapter cAdapter) Plan(request Request) (Plan, error) {
	if request.Target.Kind != "main" || !adapter.Matches(request.Target.Path) {
		return Plan{}, fmt.Errorf("unsupported C/C++ debug target %q", request.Target.Path)
	}
	project, err := dap.ResolveWorkspaceDirectory(request.WorkspaceDir, request.ProjectDir)
	if err != nil {
		return Plan{}, err
	}
	directory := filepath.Dir(filepath.FromSlash(request.Target.Path))
	sourceDir, err := dap.ResolveWorkspaceDirectory(request.WorkspaceDir, directory)
	if err != nil {
		return Plan{}, err
	}
	if _, err := projectPath(project, sourceDir); err != nil {
		return Plan{}, err
	}
	language := nativeSourceLanguage(request.Target.Path)
	compiler := cCompiler(language == "C++")
	if compiler == "" {
		variable := "CC"
		if language == "C++" {
			variable = "CXX"
		}
		return Plan{}, fmt.Errorf("a %s compiler was not found; install Clang or GCC, or set %s to its executable", language, variable)
	}
	files, err := os.ReadDir(sourceDir)
	if err != nil {
		return Plan{}, err
	}
	root, err := os.OpenRoot(sourceDir)
	if err != nil {
		return Plan{}, err
	}
	defer root.Close()
	selected := filepath.Base(request.Target.Path)
	sources := []string{"./" + selected}
	// Include sibling translation units in the same language, leaving other
	// main programs independent. Mixed C/C++ projects need a build system.
	for _, file := range files {
		if !file.Type().IsRegular() || file.Name() == selected || nativeSourceLanguage(file.Name()) != language {
			continue
		}
		source, err := root.ReadFile(file.Name())
		if err != nil {
			return Plan{}, err
		}
		targets, _ := adapter.Detect(file.Name(), source)
		if len(targets) == 0 {
			sources = append(sources, "./"+file.Name())
		}
	}
	var flags []string
	if content, err := root.ReadFile("compile_flags.txt"); err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			if flag := strings.TrimSpace(line); flag != "" {
				flags = append(flags, flag)
			}
		}
	} else if !os.IsNotExist(err) {
		return Plan{}, err
	}
	program := ".wingman-debug-" + strings.TrimSuffix(selected, filepath.Ext(selected))
	if runtime.GOOS == "windows" {
		program += ".exe"
	}
	if info, err := root.Lstat(program); err == nil && !info.Mode().IsRegular() {
		return Plan{}, fmt.Errorf("%s debug output %s is not a regular file", language, program)
	} else if err != nil && !os.IsNotExist(err) {
		return Plan{}, err
	}
	args := append(flags, "-g", "-O0")
	args = append(args, sources...)
	args = append(args, "-o", program)
	configuration := map[string]any{"program": program, "cwd": "."}
	plan := Plan{
		Title:      actionLabel(request.Action) + " " + selected,
		Summary:    fmt.Sprintf("Build the %s source files beside %s with debug symbols, then %s main.", language, selected, request.Action),
		ProjectDir: filepath.ToSlash(directory), Request: "launch", IO: dap.IOOutput, SupportsTerminal: true,
		Configuration: configuration,
		PreLaunch:     &dap.ProcessLaunch{Title: "Build " + language + " program", Command: compiler, Args: args, WaitForExit: true},
	}
	if request.Action == "run" {
		configuration["noDebug"] = true
	} else {
		plan.Breakpoints = targetBreakpoint(request.Target)
	}
	return plan, nil
}

func cCompiler(cpp bool) string {
	variable, candidates := "CC", []string{"cc", "clang", "gcc"}
	if cpp {
		variable, candidates = "CXX", []string{"c++", "clang++", "g++"}
	}
	if configured := strings.TrimSpace(os.Getenv(variable)); configured != "" {
		return absoluteCommandPath(configured)
	}
	for _, command := range candidates {
		if path := absoluteCommandPath(command); path != "" {
			return path
		}
	}
	return ""
}
