package devtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var pythonRecipes = []recipe{
	{ID: "basedpyright", Label: "Python language tools", Kind: installerPython, Packages: []string{"basedpyright"}, Commands: []string{"basedpyright", "basedpyright-langserver"}},
	{ID: "debugpy", Label: "Python debugger", Kind: installerPython, Packages: []string{"debugpy"}, Commands: []string{"debugpy-adapter"}},
}

func (m *Manager) installPython(ctx context.Context, item recipe, stage string) error {
	python := "python3"
	if runtime.GOOS == "windows" {
		python = "python"
	}
	python, err := m.look(python)
	if err != nil {
		return errors.New("python is not installed")
	}
	workingDir := installWorkingDir(item, m.root)
	if output, err := m.run(ctx, python, []string{"-m", "venv", stage}, workingDir, os.Environ()); err != nil {
		return commandError(output, err)
	}
	venvPython := filepath.Join(stage, "bin", "python")
	if runtime.GOOS == "windows" {
		venvPython = filepath.Join(stage, "Scripts", "python.exe")
	}
	args := []string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--upgrade"}
	args = append(args, item.Packages...)
	if output, err := m.run(ctx, venvPython, args, workingDir, os.Environ()); err != nil {
		return commandError(output, err)
	}
	return m.writePythonLaunchers(ctx, item, stage, venvPython)
}

var pythonEntryPointPattern = regexp.MustCompile(`^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*:[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*$`)

func (m *Manager) writePythonLaunchers(ctx context.Context, item recipe, stage, python string) error {
	const discover = `import importlib.metadata as m,json; e=m.entry_points(); e=e.select(group="console_scripts") if hasattr(e,"select") else e.get("console_scripts",[]); print(json.dumps({x.name:x.value for x in e}))`
	output, err := m.run(ctx, python, []string{"-c", discover}, stage, os.Environ())
	if err != nil {
		return commandError(output, err)
	}
	entries := make(map[string]string)
	if err := json.Unmarshal(output, &entries); err != nil {
		return fmt.Errorf("read Python console scripts: %w", err)
	}
	directory := filepath.Join(stage, "bin")
	if runtime.GOOS == "windows" {
		directory = filepath.Join(stage, "Scripts")
	}
	for _, command := range item.Commands {
		entry := strings.Fields(entries[command])
		if len(entry) == 0 || !pythonEntryPointPattern.MatchString(entry[0]) {
			return fmt.Errorf("Python package did not provide a valid %s console script", command)
		}
		for _, name := range append(commandNames(command), command+"-script.py") {
			if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		module, function, _ := strings.Cut(entry[0], ":")
		code := pythonEntryPointCode(module, function, runtime.GOOS == "windows")
		if runtime.GOOS == "windows" {
			contents := "@echo off\r\n\"%~dp0python.exe\" -c \"" + code + "\" %*\r\n"
			if err := os.WriteFile(filepath.Join(directory, command+".cmd"), []byte(contents), 0o755); err != nil {
				return err
			}
			continue
		}
		contents := "#!/bin/sh\nSCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec \"$SCRIPT_DIR/python\" -c '" + code + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(directory, command), []byte(contents), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func pythonEntryPointCode(module, function string, windows bool) string {
	quote := `"`
	if windows {
		quote = `'`
	}
	code := "import importlib,sys; f=importlib.import_module(" + quote + module + quote + ")"
	for _, attribute := range strings.Split(function, ".") {
		code += "; f=getattr(f," + quote + attribute + quote + ")"
	}
	return code + "; sys.exit(f())"
}
