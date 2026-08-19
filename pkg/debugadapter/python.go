package debugadapter

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

type pythonAdapter struct {
	fallbackCommand string
	fallbackArgs    []string
}

func newPythonAdapter() pythonAdapter {
	adapterPath := discoverBundledDebugpyAdapter()
	if adapterPath == "" {
		return pythonAdapter{}
	}
	command := "python3"
	if runtime.GOOS == "windows" {
		command = "python"
	}
	return pythonAdapter{
		fallbackCommand: command,
		fallbackArgs:    []string{adapterPath},
	}
}

func (pythonAdapter) Language() string { return "Python" }

func (adapter pythonAdapter) Descriptor() dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name:             "debugpy",
		Language:         "Python",
		AdapterID:        "python",
		Command:          "debugpy-adapter",
		FallbackCommand:  adapter.fallbackCommand,
		FallbackArgs:     slices.Clone(adapter.fallbackArgs),
		Transport:        dap.TransportStdio,
		TerminalStrategy: dap.TerminalRunInTerminal,
		Markers:          []string{"pyproject.toml", "setup.py", "requirements.txt", "Pipfile"},
		SourceExtensions: []string{".py"},
		Defaults:         map[string]any{"type": "python"},
		ConfigurationPaths: []dap.ConfigurationPath{
			{Key: "program"},
			{Key: "cwd", Directory: true},
		},
		IOConfigKey: "console",
		IOValues:    vscodeIOValues(),
	}
}

func discoverBundledDebugpyAdapter() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	patterns := []string{
		filepath.Join(home, ".vscode", "extensions", "ms-python.debugpy-*", "bundled", "libs", "debugpy", "adapter"),
		filepath.Join(home, ".vscode-insiders", "extensions", "ms-python.debugpy-*", "bundled", "libs", "debugpy", "adapter"),
		filepath.Join(home, ".cursor", "extensions", "ms-python.debugpy-*", "bundled", "libs", "debugpy", "adapter"),
		filepath.Join(home, ".windsurf", "extensions", "ms-python.debugpy-*", "bundled", "libs", "debugpy", "adapter"),
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		patterns = append(patterns,
			filepath.Join(localAppData, "Programs", "Microsoft VS Code", "resources", "app", "extensions", "ms-python.debugpy-*", "bundled", "libs", "debugpy", "adapter"),
			filepath.Join(localAppData, "Programs", "cursor", "resources", "app", "extensions", "ms-python.debugpy-*", "bundled", "libs", "debugpy", "adapter"),
		)
	}
	var matches []string
	for _, pattern := range patterns {
		values, _ := filepath.Glob(pattern)
		for _, value := range values {
			if info, err := os.Stat(filepath.Join(value, "__main__.py")); err == nil && !info.IsDir() {
				matches = append(matches, filepath.Clean(value))
			}
		}
	}
	slices.Sort(matches)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func (pythonAdapter) Matches(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".py")
}

func (pythonAdapter) Detect(path string, source []byte) ([]Target, error) {
	line := pythonEntrypointLine(path, source)
	if line == 0 {
		return nil, nil
	}
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
	if directory == "" {
		directory = "."
	}
	name := filepath.Base(filepath.FromSlash(path))
	return []Target{{
		ID: fmt.Sprintf("python:%s:script", filepath.ToSlash(path)), Name: name,
		Detail: "Python script", Kind: "script", Language: "Python", Path: filepath.ToSlash(path),
		Directory: directory, Line: line, Column: 1,
	}}, nil
}

func (pythonAdapter) Plan(request Request) (Plan, error) {
	program, err := projectPath(request.ProjectDir, request.Target.Path)
	if err != nil {
		return Plan{}, err
	}
	configuration := map[string]any{
		"program": program, "cwd": ".", "justMyCode": true, "redirectOutput": true,
	}
	plan := Plan{
		Title:      actionLabel(request.Action) + " " + request.Target.Name,
		Summary:    fmt.Sprintf("%s Python script %s.", actionLabel(request.Action), request.Target.Path),
		ProjectDir: request.ProjectDir, Request: "launch", IO: dap.IOOutput, Configuration: configuration,
	}
	if request.Action == "run" {
		configuration["noDebug"] = true
	} else {
		plan.Breakpoints = targetBreakpoint(request.Target)
	}
	return plan, nil
}

func pythonEntrypointLine(path string, source []byte) int {
	scanner := bufio.NewScanner(bytes.NewReader(source))
	firstCode := 0
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if firstCode == 0 {
			firstCode = line
		}
		compact := strings.ReplaceAll(strings.ReplaceAll(text, " ", ""), "\t", "")
		if strings.HasPrefix(compact, `if__name__=="__main__":`) || strings.HasPrefix(compact, "if__name__=='__main__':") {
			return line
		}
	}
	switch strings.ToLower(filepath.Base(filepath.FromSlash(path))) {
	case "main.py", "app.py", "cli.py", "manage.py", "__main__.py":
		return firstCode
	default:
		return 0
	}
}
