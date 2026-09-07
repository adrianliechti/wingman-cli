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
	if s.LanguageID == "cpp" && filepath.Ext(path) == ".c" {
		return "c"
	}
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
	Name     string
	Label    string
	Markers  []string
	Servers  []Server
	Excludes []string
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
		Markers: []string{"pyproject.toml", "ty.toml", "setup.py", "requirements.txt", "Pipfile", "setup.cfg"},
		Servers: []Server{
			{
				Name:       "ty",
				Command:    "ty",
				Args:       []string{"server"},
				Languages:  []string{"py", "pyi"},
				LanguageID: "python",
			},
		},
	},
	{
		Name:    "cpp",
		Label:   "C/C++",
		Markers: []string{"compile_commands.json", "compile_flags.txt", ".clangd", "CMakeLists.txt", "meson.build"},
		Servers: []Server{
			{
				Name:       "clangd",
				Command:    "clangd",
				Languages:  []string{"c", "h", "cc", "hh", "cpp", "hpp", "cxx", "hxx", "c++", "h++", "ipp", "tpp"},
				LanguageID: "cpp",
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
}
