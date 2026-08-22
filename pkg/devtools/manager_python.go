package devtools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var pythonRecipes = []recipe{
	{ID: "basedpyright", Kind: installerPython, Packages: []string{"basedpyright"}, Commands: []string{"basedpyright", "basedpyright-langserver"}},
	{ID: "debugpy", Kind: installerPython, Packages: []string{"debugpy"}, Commands: []string{"debugpy-adapter"}},
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
	if output, err := m.run(ctx, python, []string{"-m", "venv", stage}, m.root, os.Environ()); err != nil {
		return commandError(output, err)
	}
	venvPython := filepath.Join(stage, "bin", "python")
	if runtime.GOOS == "windows" {
		venvPython = filepath.Join(stage, "Scripts", "python.exe")
	}
	args := []string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--upgrade"}
	args = append(args, item.Packages...)
	if output, err := m.run(ctx, venvPython, args, stage, os.Environ()); err != nil {
		return commandError(output, err)
	}
	return nil
}
