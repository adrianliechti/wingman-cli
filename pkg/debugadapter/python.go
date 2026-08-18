package debugadapter

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

type pythonAdapter struct{}

func (pythonAdapter) Language() string { return "Python" }

func (pythonAdapter) Descriptor() dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name:             "debugpy",
		Language:         "Python",
		AdapterID:        "python",
		Command:          "debugpy-adapter",
		Transport:        dap.TransportStdio,
		TerminalStrategy: dap.TerminalRunInTerminal,
		Markers:          []string{"pyproject.toml", "setup.py", "requirements.txt", "Pipfile"},
		SourceExtensions: []string{".py"},
		Defaults:         map[string]any{"type": "python"},
		ConfigurationPaths: []dap.ConfigurationPath{
			{Key: "program"},
			{Key: "cwd", Directory: true},
		},
		ConsoleConfigKey: "console",
	}
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
		ProjectDir: request.ProjectDir, Request: "launch", Console: "internalConsole", Configuration: configuration,
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
