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
		Name:    "deno",
		Markers: []string{"deno.json", "deno.jsonc"},
		Servers: []Server{
			{
				Name:       "deno",
				Command:    "deno",
				Args:       []string{"lsp"},
				Languages:  []string{"ts", "tsx", "js", "jsx", "mjs"},
				LanguageID: "typescript",
			},
		},
	},
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
			{
				Name:       "vtsls",
				Command:    "vtsls",
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
			{
				Name:       "pyright",
				Command:    "pyright-langserver",
				Args:       []string{"--stdio"},
				Languages:  []string{"py", "pyi"},
				LanguageID: "python",
			},
			{
				Name:       "pylsp",
				Command:    "pylsp",
				Args:       []string{},
				Languages:  []string{"py", "pyi"},
				LanguageID: "python",
			},
			{
				Name:       "jedi-language-server",
				Command:    "jedi-language-server",
				Args:       []string{},
				Languages:  []string{"py", "pyi"},
				LanguageID: "python",
			},
		},
	},
	{
		Name:    "cpp",
		Label:   "C/C++",
		Markers: []string{"compile_commands.json", "CMakeLists.txt", ".clangd"},
		Servers: []Server{
			{
				Name:       "clangd",
				Command:    "clangd",
				Args:       []string{"--background-index", "--clang-tidy"},
				Languages:  []string{"c", "h", "cpp", "hpp", "cc", "cxx", "hxx", "c++", "h++", "hh"},
				LanguageID: "cpp",
			},
			{
				Name:       "ccls",
				Command:    "ccls",
				Args:       []string{},
				Languages:  []string{"c", "h", "cpp", "hpp", "cc", "cxx", "hxx", "c++", "h++", "hh"},
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
				Name:       "omnisharp",
				Command:    "OmniSharp",
				Args:       []string{"-lsp"},
				Languages:  []string{"cs"},
				LanguageID: "csharp",
			},
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
		Name:    "fsharp",
		Label:   "F#",
		Markers: []string{"*.fsproj", "*.sln", "*.slnx", "global.json"},
		Servers: []Server{
			{
				Name:       "fsautocomplete",
				Command:    "fsautocomplete",
				Args:       []string{},
				Languages:  []string{"fs", "fsi", "fsx"},
				LanguageID: "fsharp",
			},
		},
	},
	{
		Name:    "ruby",
		Markers: []string{"Gemfile", ".ruby-version", "Rakefile"},
		Servers: []Server{
			{
				Name:       "ruby-lsp",
				Command:    "ruby-lsp",
				Args:       []string{},
				Languages:  []string{"rb", "rake", "gemspec", "ru"},
				LanguageID: "ruby",
			},
			{
				Name:       "solargraph",
				Command:    "solargraph",
				Args:       []string{"stdio"},
				Languages:  []string{"rb", "rake", "gemspec", "ru"},
				LanguageID: "ruby",
			},
		},
	},
	{
		Name:    "php",
		Label:   "PHP",
		Markers: []string{"composer.json", "artisan"},
		Servers: []Server{
			{
				Name:       "intelephense",
				Command:    "intelephense",
				Args:       []string{"--stdio"},
				Languages:  []string{"php"},
				LanguageID: "php",
			},
			{
				Name:       "phpactor",
				Command:    "phpactor",
				Args:       []string{"language-server"},
				Languages:  []string{"php"},
				LanguageID: "php",
			},
		},
	},
	{
		Name:    "zig",
		Markers: []string{"build.zig", "zls.json"},
		Servers: []Server{
			{
				Name:       "zls",
				Command:    "zls",
				Args:       []string{},
				Languages:  []string{"zig", "zon"},
				LanguageID: "zig",
			},
		},
	},
	{
		Name:    "lua",
		Markers: []string{".luarc.json", ".luarc.jsonc", ".luacheckrc"},
		Servers: []Server{
			{
				Name:       "lua-language-server",
				Command:    "lua-language-server",
				Args:       []string{},
				Languages:  []string{"lua"},
				LanguageID: "lua",
			},
		},
	},
	{
		Name:     "kotlin",
		Label:    "Kotlin",
		Markers:  []string{"settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts", "pom.xml"},
		Requires: []string{"*.kt", "*.main.kts"},
		Servers: []Server{
			{
				Name:       "kotlin-lsp",
				Command:    "kotlin-lsp",
				Args:       []string{"--stdio"},
				Languages:  []string{"kt", "kts"},
				LanguageID: "kotlin",
			},
			{
				Name:       "kotlin-language-server",
				Command:    "kotlin-language-server",
				Args:       []string{},
				Languages:  []string{"kt", "kts"},
				LanguageID: "kotlin",
			},
		},
	},
	{
		Name:    "swift",
		Markers: []string{"Package.swift"},
		Servers: []Server{
			{
				Name:       "sourcekit-lsp",
				Command:    "sourcekit-lsp",
				Args:       []string{},
				Languages:  []string{"swift"},
				LanguageID: "swift",
			},
		},
	},
	{
		Name:    "elixir",
		Markers: []string{"mix.exs", "mix.lock"},
		Servers: []Server{
			{
				Name:       "elixir-ls",
				Command:    "elixir-ls",
				Args:       []string{},
				Languages:  []string{"ex", "exs"},
				LanguageID: "elixir",
			},
			{
				Name:       "lexical",
				Command:    "lexical",
				Args:       []string{},
				Languages:  []string{"ex", "exs"},
				LanguageID: "elixir",
			},
		},
	},
	{
		Name:    "haskell",
		Markers: []string{"stack.yaml", "cabal.project", "hie.yaml", "*.cabal"},
		Servers: []Server{
			{
				Name:       "haskell-language-server",
				Command:    "haskell-language-server-wrapper",
				Args:       []string{"--lsp"},
				Languages:  []string{"hs", "lhs"},
				LanguageID: "haskell",
			},
		},
	},
	{
		Name:    "scala",
		Markers: []string{"build.sbt", ".metals", "build.sc"},
		Servers: []Server{
			{
				Name:       "metals",
				Command:    "metals",
				Args:       []string{},
				Languages:  []string{"scala", "sc"},
				LanguageID: "scala",
			},
		},
	},
	{
		Name:    "dart",
		Markers: []string{"pubspec.yaml", "analysis_options.yaml"},
		Servers: []Server{
			{
				Name:       "dart",
				Command:    "dart",
				Args:       []string{"language-server", "--lsp"},
				Languages:  []string{"dart"},
				LanguageID: "dart",
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
		Name:    "terraform",
		Markers: []string{"main.tf", "terraform.tf", ".terraform", ".terraform.lock.hcl"},
		Servers: []Server{
			{
				Name:       "terraform-ls",
				Command:    "terraform-ls",
				Args:       []string{"serve"},
				Languages:  []string{"tf", "tfvars"},
				LanguageID: "terraform",
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
	{
		Name:    "ocaml",
		Label:   "OCaml",
		Markers: []string{"dune-project", "dune-workspace", ".merlin", "*.opam"},
		Servers: []Server{
			{
				Name:       "ocamllsp",
				Command:    "ocamllsp",
				Args:       []string{},
				Languages:  []string{"ml", "mli"},
				LanguageID: "ocaml",
			},
		},
	},
	{
		Name:    "gleam",
		Markers: []string{"gleam.toml"},
		Servers: []Server{
			{
				Name:       "gleam",
				Command:    "gleam",
				Args:       []string{"lsp"},
				Languages:  []string{"gleam"},
				LanguageID: "gleam",
			},
		},
	},
	{
		Name:    "clojure",
		Markers: []string{"deps.edn", "project.clj", "shadow-cljs.edn", "bb.edn"},
		Servers: []Server{
			{
				Name:       "clojure-lsp",
				Command:    "clojure-lsp",
				Args:       []string{"listen"},
				Languages:  []string{"clj", "cljs", "cljc", "edn"},
				LanguageID: "clojure",
			},
		},
	},
	{
		Name:    "nix",
		Markers: []string{"flake.nix", "default.nix", "shell.nix"},
		Servers: []Server{
			{
				Name:       "nixd",
				Command:    "nixd",
				Args:       []string{},
				Languages:  []string{"nix"},
				LanguageID: "nix",
			},
		},
	},
	{
		Name:    "typst",
		Markers: []string{"typst.toml"},
		Servers: []Server{
			{
				Name:       "tinymist",
				Command:    "tinymist",
				Args:       []string{},
				Languages:  []string{"typ"},
				LanguageID: "typst",
			},
		},
	},
	{
		Name:    "latex",
		Label:   "LaTeX",
		Markers: []string{".latexmkrc", "latexmkrc", ".texlabroot"},
		Servers: []Server{
			{
				Name:       "texlab",
				Command:    "texlab",
				Args:       []string{},
				Languages:  []string{"tex", "bib"},
				LanguageID: "latex",
			},
		},
	},
}
