package devtools

import (
	"context"
	"errors"
	"os"
)

var nodeRecipes = []recipe{
	{ID: "typescript-language-server", Kind: installerNPM, Packages: []string{"typescript-language-server@latest", "typescript@latest"}, Commands: []string{"tsc", "typescript-language-server"}},
	{ID: "intelephense", Kind: installerNPM, Packages: []string{"intelephense@latest"}, Commands: []string{"intelephense"}},
	{ID: "vue-language-server", Kind: installerNPM, Packages: []string{"@vue/language-server@latest", "typescript@latest"}, Commands: []string{"vue-language-server"}},
	{ID: "svelte-language-server", Kind: installerNPM, Packages: []string{"svelte-language-server@latest", "typescript@latest"}, Commands: []string{"svelteserver"}},
	{ID: "astro-language-server", Kind: installerNPM, Packages: []string{"@astrojs/language-server@latest", "typescript@latest"}, Commands: []string{"astro-ls"}},
	{ID: "bash-language-server", Kind: installerNPM, Packages: []string{"bash-language-server@latest"}, Commands: []string{"bash-language-server"}},
	{ID: "yaml-language-server", Kind: installerNPM, Packages: []string{"yaml-language-server@latest"}, Commands: []string{"yaml-language-server"}},
	{ID: "dockerfile-language-server", Kind: installerNPM, Packages: []string{"dockerfile-language-server-nodejs@latest"}, Commands: []string{"docker-langserver"}},
	{ID: "prisma-language-server", Kind: installerNPM, Packages: []string{"prisma@latest"}, Commands: []string{"prisma"}},
}

func (m *Manager) installNPM(ctx context.Context, item recipe, stage string) error {
	npm, err := m.look("npm")
	if err != nil {
		return errors.New("npm is not installed")
	}
	args := []string{"install", "--prefix", stage, "--no-package-lock", "--no-audit", "--no-fund", "--omit=dev"}
	args = append(args, item.Packages...)
	if output, err := m.run(ctx, npm, args, stage, os.Environ()); err != nil {
		return commandError(output, err)
	}
	return nil
}
