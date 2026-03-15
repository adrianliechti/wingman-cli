package lsp

// Server describes an LSP server binary and how to invoke it.
type Server struct {
	Name       string   // Display name (e.g., "gopls")
	Command    string   // Binary name (e.g., "gopls")
	Args       []string // Arguments (e.g., ["serve"])
	Languages  []string // File extensions without dot (e.g., ["go"])
	LanguageID string   // LSP language identifier (e.g., "go")
}

// ProjectType maps project markers to LSP server candidates.
type ProjectType struct {
	Name    string   // Project type name (e.g., "go")
	Markers []string // Files that indicate this project type (e.g., ["go.mod"])
	Servers []Server // Server candidates in priority order (first available wins)
}

// knownProjects contains the registry of known project types and their LSP servers.
var knownProjects = []ProjectType{
	// Go
	{
		Name:    "go",
		Markers: []string{"go.mod", "go.work"},
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
	// TypeScript / JavaScript
	{
		Name:    "typescript",
		Markers: []string{"tsconfig.json", "jsconfig.json", "package.json"},
		Servers: []Server{
			{
				Name:       "typescript-language-server",
				Command:    "typescript-language-server",
				Args:       []string{"--stdio"},
				Languages:  []string{"ts", "tsx", "js", "jsx", "mjs", "cjs"},
				LanguageID: "typescript",
			},
			{
				Name:       "vtsls",
				Command:    "vtsls",
				Args:       []string{"--stdio"},
				Languages:  []string{"ts", "tsx", "js", "jsx", "mjs", "cjs"},
				LanguageID: "typescript",
			},
		},
	},
	// Rust
	{
		Name:    "rust",
		Markers: []string{"Cargo.toml"},
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
		Markers: []string{"pyproject.toml", "setup.py", "requirements.txt", "Pipfile", "setup.cfg"},
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
				Args:       []string{},
				Languages:  []string{"c", "h", "cpp", "hpp", "cc", "cxx", "hxx"},
				LanguageID: "cpp",
			},
			{
				Name:       "ccls",
				Command:    "ccls",
				Args:       []string{},
				Languages:  []string{"c", "h", "cpp", "hpp", "cc", "cxx", "hxx"},
				LanguageID: "cpp",
			},
		},
	},
	// Java
	{
		Name:    "java",
		Markers: []string{"pom.xml", "build.gradle", "build.gradle.kts", ".project"},
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
		Markers: []string{"*.csproj", "*.sln", "global.json"},
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
	// Ruby
	{
		Name:    "ruby",
		Markers: []string{"Gemfile", ".ruby-version", "Rakefile"},
		Servers: []Server{
			{
				Name:       "ruby-lsp",
				Command:    "ruby-lsp",
				Args:       []string{},
				Languages:  []string{"rb", "rake", "gemspec"},
				LanguageID: "ruby",
			},
			{
				Name:       "solargraph",
				Command:    "solargraph",
				Args:       []string{"stdio"},
				Languages:  []string{"rb", "rake", "gemspec"},
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
				Languages:  []string{"zig"},
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
		Markers: []string{"build.gradle.kts", "settings.gradle.kts"},
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
		Markers: []string{"mix.exs"},
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
		Markers: []string{"stack.yaml", "cabal.project", "hie.yaml"},
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
	// Terraform
	{
		Name:    "terraform",
		Markers: []string{"main.tf", "terraform.tf", ".terraform"},
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
}
