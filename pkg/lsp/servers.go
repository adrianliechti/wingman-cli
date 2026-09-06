package lsp

import (
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

type Server struct {
	Name                  string
	Label                 string
	Command               string
	Args                  []string
	Languages             []string
	LanguageID            string
	MinimumMajorVersion   int
	InitializationOptions []byte
}

func (s Server) LanguageIDForPath(path string) string {
	if s.LanguageID != "typescript" {
		return s.LanguageID
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".tsx":
		return "typescriptreact"
	default:
		return "typescript"
	}
}

type projectType struct {
	Name          string
	Label         string
	WorkspaceRoot bool
	Markers       []string
	Servers       []Server
	Excludes      []string
	Requires      []string
}

var knownProjects = []projectType{

	{
		Name:     "typescript",
		Label:    "TypeScript",
		Markers:  []string{"tsconfig.json", "jsconfig.json", "package.json", "package-lock.json", "bun.lock", "bun.lockb", "yarn.lock", "pnpm-lock.yaml"},
		Excludes: []string{"deno.json", "deno.jsonc"},
		Servers: []Server{
			{
				Name:                "typescript-go",
				Command:             "tsc",
				Args:                []string{"--lsp", "--stdio"},
				Languages:           []string{"ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts"},
				LanguageID:          "typescript",
				MinimumMajorVersion: 7,
			},
			{
				Name:       "typescript-language-server",
				Command:    "typescript-language-server",
				Args:       []string{"--stdio"},
				Languages:  []string{"ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts"},
				LanguageID: "typescript",
			},
		},
	},
	{
		Name:    "go",
		Markers: []string{"go.mod", "go.work", "go.sum"},
		Servers: []Server{
			{
				Name:       "gopls",
				Command:    "gopls",
				Args:       []string{"serve"},
				Languages:  []string{"go"},
				LanguageID: "go",
			},
		},
	},
	{
		Name:    "rust",
		Markers: []string{"Cargo.toml", "Cargo.lock"},
		Servers: []Server{
			{
				Name:      "rust-analyzer",
				Command:   "rust-analyzer",
				Args:      []string{},
				Languages: []string{"rs"},
				// rustup ships a rust-analyzer proxy even when the component is
				// missing; only a command that actually runs may satisfy detection.
				MinimumMajorVersion: tooling.ProbeExecutes,
				LanguageID:          "rust",
			},
		},
	},
	{
		Name:    "python",
		Markers: []string{"pyproject.toml", "setup.py", "requirements.txt", "Pipfile", "setup.cfg", "pyrightconfig.json"},
		Servers: []Server{
			{
				Name:       "basedpyright",
				Command:    "basedpyright-langserver",
				Args:       []string{"--stdio"},
				Languages:  []string{"py", "pyi"},
				LanguageID: "python",
			},
		},
	},
	{
		Name:    "java",
		Markers: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", ".project", ".classpath"},
		Servers: []Server{
			{
				Name:       "jdtls",
				Command:    "jdtls",
				Args:       []string{},
				Languages:  []string{"java"},
				LanguageID: "java",
			},
		},
	},
	{
		Name:    "csharp",
		Label:   "C#",
		Markers: []string{"*.csproj", "*.sln", "*.slnx", "global.json"},
		Servers: []Server{
			{
				Name:       "csharp-ls",
				Command:    "csharp-ls",
				Args:       []string{},
				Languages:  []string{"cs"},
				LanguageID: "csharp",
			},
		},
	},
	{
		Name:     "vue",
		Markers:  []string{"package.json", "package-lock.json", "bun.lock", "bun.lockb", "yarn.lock", "pnpm-lock.yaml"},
		Requires: []string{"*.vue"},
		Servers: []Server{
			{
				Name:       "vue-language-server",
				Command:    "vue-language-server",
				Args:       []string{"--stdio"},
				Languages:  []string{"vue"},
				LanguageID: "vue",
			},
		},
	},
	{
		Name:     "svelte",
		Markers:  []string{"package.json", "package-lock.json", "bun.lock", "bun.lockb", "yarn.lock", "pnpm-lock.yaml"},
		Requires: []string{"*.svelte"},
		Servers: []Server{
			{
				Name:       "svelteserver",
				Command:    "svelteserver",
				Args:       []string{"--stdio"},
				Languages:  []string{"svelte"},
				LanguageID: "svelte",
			},
		},
	},
	{
		Name:     "astro",
		Markers:  []string{"package.json", "package-lock.json", "bun.lock", "bun.lockb", "yarn.lock", "pnpm-lock.yaml"},
		Requires: []string{"*.astro"},
		Servers: []Server{
			{
				Name:       "astro-ls",
				Command:    "astro-ls",
				Args:       []string{"--stdio"},
				Languages:  []string{"astro"},
				LanguageID: "astro",
			},
		},
	},
	{
		Name:          "bash",
		WorkspaceRoot: true,
		Markers:       []string{".bashrc", ".bash_profile", ".zshrc", "*.sh", "*.bash", "*.zsh", "*.ksh"},
		Servers: []Server{
			{
				Name:       "bash-language-server",
				Command:    "bash-language-server",
				Args:       []string{"start"},
				Languages:  []string{"sh", "bash", "zsh", "ksh"},
				LanguageID: "shellscript",
			},
		},
	},
	{
		Name:          "yaml",
		Label:         "YAML",
		WorkspaceRoot: true,
		Markers:       []string{"*.yaml", "*.yml"},
		Servers: []Server{
			{
				Name:       "yaml-language-server",
				Command:    "yaml-language-server",
				Args:       []string{"--stdio"},
				Languages:  []string{"yaml", "yml"},
				LanguageID: "yaml",
			},
		},
	},
	{
		Name:    "docker",
		Markers: []string{"Dockerfile", "Containerfile"},
		Servers: []Server{
			{
				Name:       "docker-langserver",
				Command:    "docker-langserver",
				Args:       []string{"--stdio"},
				Languages:  []string{"dockerfile"},
				LanguageID: "dockerfile",
			},
		},
	},
}
