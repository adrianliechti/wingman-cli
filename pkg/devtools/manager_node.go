package devtools

import (
	"context"
	"errors"
	"os"
)

var nodeRecipes = []recipe{
	{ID: "typescript-language-server", Label: "TypeScript language tools", Kind: installerNPM, Packages: []string{"typescript-language-server@latest", "typescript@latest"}, Commands: []string{"tsc", "typescript-language-server"}},
}

func (m *Manager) installNPM(ctx context.Context, item recipe, stage string) (string, error) {
	npm, err := m.look("npm")
	if err != nil {
		return "", errors.New("npm is not installed")
	}
	args := []string{"install", "--prefix", stage, "--no-package-lock", "--no-audit", "--no-fund", "--omit=dev"}
	args = append(args, item.Packages...)
	if output, err := m.run(ctx, npm, args, installWorkingDir(item, stage), os.Environ()); err != nil {
		return "", commandError(output, err)
	}
	return "", nil
}
