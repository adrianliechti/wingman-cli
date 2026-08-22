package devtools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

var goRecipes = []recipe{
	{ID: "gopls", Kind: installerGo, Packages: []string{"golang.org/x/tools/gopls@latest"}, Commands: []string{"gopls"}},
	{ID: "delve", Kind: installerGo, Packages: []string{"github.com/go-delve/delve/cmd/dlv@latest"}, Commands: []string{"dlv"}},
}

func (m *Manager) installGo(ctx context.Context, item recipe, stage string) error {
	goCommand, err := m.look("go")
	if err != nil {
		return errors.New("go is not installed")
	}
	bin := filepath.Join(stage, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	for _, pkg := range item.Packages {
		if output, err := m.run(ctx, goCommand, []string{"install", pkg}, stage, append(os.Environ(), "GOBIN="+bin)); err != nil {
			return commandError(output, err)
		}
	}
	return nil
}
