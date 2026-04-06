package lsp

// Server describes an LSP server binary and how to invoke it.
type Server struct {
	Name       string   // Display name (e.g., "gopls")
	Command    string   // Binary name (e.g., "gopls")
	Args       []string // Arguments (e.g., ["serve"])
	Languages  []string // File extensions without dot (e.g., ["go"])
	LanguageID string   // LSP language identifier (e.g., "go")
}

// projectType maps project markers to LSP server candidates.
type projectType struct {
	Name     string   // Project type name (e.g., "go")
	Markers  []string // Files that indicate this project type (e.g., ["go.mod"])
	Servers  []Server // Server candidates in priority order (first available wins)
	Excludes []string // Marker files that exclude this project type (e.g., Deno excludes TS)
}

// knownProjects contains the registry of known project types and their LSP servers.
var knownProjects = []projectType{
	// Deno (must be before TypeScript so it takes priority when deno.json is present)
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
	// TypeScript / JavaScript
	{
		Name:     "typescript",
		Markers:  []string{"tsconfig.json", "jsconfig.json", "package.json", "package-lock.json", "bun.lockb", "yarn.lock", "pnpm-lock.yaml"},
		Excludes: []string{"deno.json", "deno.jsonc"},
		Servers: []Server{
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
	// Go
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
	// Rust
	{
		Name:    "rust",
		Markers: []string{"Cargo.toml", "Cargo.lock"},
		Servers: []Server{
			{
				Name:       "rust-analyzer",
				Command:    "rust-analyzer",
				Args:       []string{},
				Languages:  []string{"rs"},
				LanguageID: "rust",
			},
		},
	},
	// Python
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
	// C/C++
	{
		Name:    "cpp",
		Markers: []string{"compile_commands.json", "CMakeLists.txt", ".clangd", "Makefile"},
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
	// Java
	{
		Name:    "java",
		Markers: []string{"pom.xml", "build.gradle", "build.gradle.kts", ".project", ".classpath"},
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
	// C# / .NET
	{
		Name:    "csharp",
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
	// F#
	{
		Name:    "fsharp",
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
	// Ruby
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
	// PHP
	{
		Name:    "php",
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
	// Zig
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
	// Lua
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
	// Kotlin
	{
		Name:    "kotlin",
		Markers: []string{"settings.gradle.kts", "build.gradle.kts"},
		Servers: []Server{
			{
				Name:       "kotlin-language-server",
				Command:    "kotlin-language-server",
				Args:       []string{},
				Languages:  []string{"kt", "kts"},
				LanguageID: "kotlin",
			},
		},
	},
	// Swift
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
	// Elixir
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
	// Haskell
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
	// Scala
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
	// Dart
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
	// Vue
	{
		Name:    "vue",
		Markers: []string{"package.json", "package-lock.json", "bun.lockb", "yarn.lock", "pnpm-lock.yaml"},
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
	// Svelte
	{
		Name:    "svelte",
		Markers: []string{"package.json", "package-lock.json", "bun.lockb", "yarn.lock", "pnpm-lock.yaml"},
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
	// Astro
	{
		Name:    "astro",
		Markers: []string{"package.json", "package-lock.json", "bun.lockb", "yarn.lock", "pnpm-lock.yaml"},
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
	// Bash / Shell
	{
		Name:    "bash",
		Markers: []string{".bashrc", ".bash_profile", ".zshrc", "*.sh"},
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
	// Terraform
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
	// YAML
	{
		Name:    "yaml",
		Markers: []string{".yamllint", "mkdocs.yml", "docker-compose.yml"},
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
	// Dockerfile
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
	// OCaml
	{
		Name:    "ocaml",
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
	// Gleam
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
	// Clojure
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
	// Nix
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
	// Prisma
	{
		Name:    "prisma",
		Markers: []string{"schema.prisma"},
		Servers: []Server{
			{
				Name:       "prisma",
				Command:    "prisma",
				Args:       []string{"language-server"},
				Languages:  []string{"prisma"},
				LanguageID: "prisma",
			},
		},
	},
	// Typst
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
	// LaTeX
	{
		Name:    "latex",
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
