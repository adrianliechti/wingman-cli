# Wingman Agent

A powerful AI-powered coding assistant that runs directly in your terminal. Wingman helps you with coding tasks by reading files, executing commands, editing code, and writing new files — all through natural conversation.

![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)
![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)

## ✨ Features

- **Interactive TUI** — Rich terminal interface with markdown rendering and syntax highlighting
- **File Operations** — Read, write, edit, and search files in your codebase
- **Shell Integration** — Execute shell commands with user approval
- **LSP Integration** — Code intelligence via auto-detected language servers (definitions, references, diagnostics, call hierarchy, and more)
- **Predictive Tab Edits** — Low-latency inline and multiline next-edit suggestions in the web editor, with import cleanup through the active language server
- **Integrated Debugging** — Debug Adapter Protocol sessions with deterministic launch profiles, breakpoints, stepping, stack/variable inspection, output, and interactive terminals
- **MCP Support** — Extend functionality with Model Context Protocol servers
- **Multi-Model Support** — Works with any [OpenResponses API](https://www.openresponses.org) compatible endpoint with auto-selection
- **Changes** — Git-backed working tree changes with a visual diff viewer
- **Skills** — Define custom workflows using [Agent Skills](https://agentskills.io) format
- **Plugins** — Install skills, MCP servers, and lifecycle hooks from Agent Plugins and Codex/Claude-compatible packages
- **Image Support** — Paste images from clipboard for vision-capable models
- **File Context** — Add files to context with `@` or drag-and-drop file paths
- **Automatic Colors** — Adapts the built-in palette to light or dark terminal backgrounds
- **Session Management** — Conversations are saved automatically and can be resumed

## 📦 Installation

### Homebrew (macOS)

```bash
brew install adrianliechti/tap/wingman-cli
```

> Linux: Homebrew no longer supports formula-style binary installs from taps, so use `go install` (below) or download a binary from the [releases](https://github.com/adrianliechti/wingman-agent/releases).

### Desktop App (macOS, Apple Silicon)

Install the Wingman Agent desktop app into `/Applications` via Homebrew Cask:

```bash
brew install --cask adrianliechti/tap/wingman-app
```

### Scoop (Windows)

```bash
scoop bucket add adrianliechti https://github.com/adrianliechti/scoop-bucket
scoop install wingman
```

### From Source

```bash
go install github.com/adrianliechti/wingman-agent/cmd/wingman@latest
```

### Build Locally

```bash
git clone https://github.com/adrianliechti/wingman-agent.git
cd wingman-agent
go build -o wingman ./cmd/wingman
```

## 🚀 Quick Start

1. **Set up your API key:**

```bash
# For any OpenAI-compatible API endpoint
export OPENAI_API_KEY="your-api-key"

# Optional: custom endpoint (defaults to OpenAI)
export OPENAI_BASE_URL="https://your-api-endpoint/v1"
```

2. **Run Wingman in your project directory:**
```bash
wingman
```

To use the Wingman terminal UI with an existing native Codex, Claude, or Pi
configuration (including subscription logins), sign in or configure the
corresponding CLI and select it at startup:

```bash
codex login
wingman --agent codex

claude auth login
wingman --agent claude

wingman --agent pi
```

These modes use the native CLI's active login and session storage rather than
the Wingman/OpenAI-compatible API configuration above. They inherit the current
shell environment unchanged, so unset API-key or alternate-provider variables
if you want the native CLI to use its stored subscription login.

The web UI uses the same agent registry and native login paths as the TUI.
Detected Claude, Codex, Copilot, OpenCode, and Pi installations are offered in
both. Additional ACP agents can be configured in `~/.wingman/agents.json`; they
are merged with detected agents and replace a detected entry only when they use
the same normalized name. The built-in **Wingman** entry continues to use the
configured API backend.

3. **Start chatting!** Ask Wingman to help with coding tasks:

```
> Show me all TODO comments in this project
> Refactor the config package to use dependency injection
> Write tests for the agent module
```

4. **Resume a previous session:**
```bash
wingman --continue            # resume the most recent session
wingman --resume <session-id> # resume a specific session

wingman --agent codex --continue  # resume the latest native Codex session
wingman --agent claude --continue # resume the latest native Claude session
wingman --agent pi --continue     # resume the latest native Pi session
```

### Non-interactive mode

Use `wingman exec` (or `wingman e`) to run a prompt without opening the TUI.
Exec always runs unattended: actions are approved automatically, and
elicitation uses defaults or recommended choices instead of waiting for a UI.
Required free-text questions are declined rather than answered with invented
input.

```bash
wingman exec "Summarize this project"
wingman exec "Fix the failing tests"
git diff | wingman exec "Review this diff for bugs"
```

When stdin is piped alongside a prompt, Wingman treats it as context and keeps
the positional prompt as the instruction. Use `-` when stdin should be the
entire prompt. Input is capped at 10 MiB.

```bash
git diff | wingman exec "Review this diff for bugs"
printf 'Summarize this project' | wingman exec -
```

The final assistant message is the only content written to stdout, and normal
runs are otherwise quiet. Add `--debug` to stream reasoning, tool arguments,
and tool results to stderr. `--json` runs a final tool-free formatting pass and
returns one JSON object; `--schema` additionally constrains that object with a
JSON Schema:

```bash
wingman exec "Generate release notes" > release-notes.md
wingman exec --debug "Inspect the main packages"
wingman exec --json "Inspect the main packages" | jq
wingman exec --schema ./project.schema.json "Extract project metadata"
```

Sessions are saved by default. Resume one by ID or continue the latest session
for the current agent and workspace:

```bash
wingman exec resume <session-id> "Now suggest improvements"
wingman exec resume --last "Implement the first suggestion"
wingman exec --ephemeral "Triage this repository"
```

### Agent Modes

| Command | UI/protocol | Model backend |
|---------|-------------|---------------|
| `wingman` or `wingman --agent wingman` | TUI | Built-in Wingman configuration |
| `wingman --agent <name>` | TUI | Native detected CLI/login, or the matching `agents.json` ACP command |
| `wingman server` | Web UI with the same agent picker | Same shared registry and behavior as the TUI |
| `wingman acp` or `wingman acp wingman` | Wingman over ACP stdio | Built-in Wingman configuration |
| `wingman acp {claude,codex,pi}` | Native agent bridge over ACP stdio | Native CLI configuration/login |
| `wingman acp {claude,codex,pi} --backend wingman` | Agent bridge over ACP stdio | Wingman backend via `WINGMAN_URL` |
| `wingman run <target>` | Wrapped external CLI | Wingman via `WINGMAN_URL` |

`wingman acp <target>` defaults to the native backend. This keeps subscription
login and session behavior aligned with selecting the same agent in the TUI or
Web UI; `--backend wingman` is the explicit opt-in to provider overrides.

## ⚙️ Configuration

### Environment Variables

**Backend** — connect to a Wingman server, Ollama, or any OpenAI-compatible API:

| Variable | Description |
|----------|-------------|
| `WINGMAN_URL` | Wingman server URL (takes priority over Ollama and OpenAI variables) |
| `WINGMAN_TOKEN` | Wingman server authentication token |
| `OPENAI_API_KEY` | API key for an OpenAI-compatible backend |
| `OPENAI_BASE_URL` | OpenAI-compatible API endpoint (default: `https://api.openai.com/v1`) |
| `OPENROUTER_API_KEY` | OpenRouter API key; connects to `https://openrouter.ai/api/v1` |
| `OLLAMA_HOST` | Ollama server host; used when Wingman, OpenAI, and OpenRouter backends are unset |
| `OLLAMA_API_KEY` | Optional Ollama API key; selects `https://ollama.com/v1` when `OLLAMA_HOST` is unset |

Provider priority is `WINGMAN_URL`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, then Ollama
(`OLLAMA_HOST` or `OLLAMA_API_KEY`). When none is set, Wingman connects to an OpenAI-compatible
backend at `http://localhost:4242/v1`.

**Models & Reasoning** — every value is optional; unset values are chosen automatically by role (plan → largest available model, code → medium, utilities → smallest):

| Variable | Description |
|----------|-------------|
| `WINGMAN_MODEL` | Coding model; takes priority over `OPENAI_DEFAULT_MODEL` |
| `WINGMAN_MODEL_PLAN` | Plan-mode model (default: largest available, e.g. Opus/Sol) |
| `WINGMAN_MODEL_UTILITY` | Model for recaps and compaction summaries (default: smallest available, e.g. Haiku/Luna) |
| `WINGMAN_MODEL_TAB` | Optional model override for web-editor Tab predictions (default: the utility role, then the current coding model) |
| `WINGMAN_EFFORT` | Coding reasoning effort: `none`/`low`/`medium`/`high`/`xhigh`/`max` (default: `high`) |
| `WINGMAN_EFFORT_PLAN` | Plan-mode reasoning effort (default: `xhigh` on large models, else `high`) |
| `WINGMAN_LARGE_CONTEXT` | `1` compacts against the model's full context window instead of stopping at the provider's long-context price threshold |

**Behavior**

| Variable | Description |
|----------|-------------|
| `WINGMAN_SANDBOX` | `off` lifts the workspace path restriction from the file tools |
| `WINGMAN_ELICITATION` | Headless (ACP) sessions: `accept` or `cancel` answers elicitation prompts automatically |
| `WINGMAN_HOME` | Overrides the `~/.wingman` directory for all Wingman-owned user data |
| `WINGMAN_<AGENT>_PATH` | Path override for an external agent binary (e.g. `WINGMAN_CODEX_PATH`) |

### User Data

`editor.tab.completion` is on by default and can be disabled or re-enabled from
the command palette. The preference is stored in `~/.wingman/config.json`.
Completions use model requests while you type; requests are edit-gated,
debounced, and limited server-wide to one active request and one start every
1.5 seconds.

Wingman's `~/.wingman/config.json` stores this preference and recent launcher
workspaces only;
backend URLs and authentication tokens are read from environment variables and
are never persisted in this file.
Set `WINGMAN_HOME` to relocate the complete `~/.wingman` directory, including
settings, project memory and sessions, global MCP configuration, skills,
plugins, and plugin data.

### Project Configuration

Create an `AGENTS.md` (or `CLAUDE.md`) file in your project root to provide context-specific instructions. Wingman walks up from your working directory and reads all matching files it finds, so you can layer project and workspace-level guidelines:

```markdown
# Project Guidelines

- Use Go 1.25+ features
- Follow standard Go project layout
- Write tests for all new functionality
```

### MCP Integration

Add an `mcp.json` file to integrate with MCP servers:

```json
{
  "mcpServers": {
    "my-server": {
      "command": "npx",
      "args": ["-y", "@my-org/my-mcp-server"]
    }
  }
}
```

Remote (HTTP/SSE) servers are also supported via the `url` and optional `headers` fields.

Configs are loaded from two locations and merged: `~/.wingman/mcp.json` (global, shared across all projects) and `./mcp.json` (project root). When a server name appears in both, the project config wins.

### Debugging (experimental)

The web editor keeps each session in one center **Debug** tab. **Debug output**
is its default view; interactive sessions start in a terminal view and can
switch back to output from the tab toolbar. A collapsible, resizable details
pane in that same tab contains variables above the call stack. While a session
is active, its transport controls replace the passive workspace name in the
Files header, so continue, pause, stepping, and stop remain available while a
source file is open. The right-hand **Inspect** view is reserved for LSP
diagnostics.

The inline **Run | Debug** CodeLens actions above supported entry points are the
only way to create a session. The selected entry point determines the adapter
and target; there is no separate adapter/target picker or command-palette launch
path. Wingman prepares a deterministic language-specific configuration and
shows its I/O mode, adapter-defined arguments, and initial pause for review
before application code executes.

Supported launch profiles and their adapters are:

| Language/runtime | Adapter | Detected targets |
|------------------|---------|------------------|
| Go | [Delve](https://github.com/go-delve/delve/tree/master/Documentation/api/dap) | `main`, test, benchmark, fuzz, and runnable example functions |
| Python | [debugpy](https://github.com/microsoft/debugpy) | Explicit `__main__` guards and conventional scripts |
| Java | [Microsoft java-debug](https://github.com/microsoft/java-debug) through JDT LS | Qualified `public static void main` classes |
| Rust | [CodeLLDB](https://github.com/vadimcn/codelldb) | Cargo binaries and examples |
| C#/.NET | [NetCoreDbg](https://github.com/Samsung/netcoredbg) | `Main` methods and top-level `Program.cs` files |
| JavaScript/TypeScript | [vscode-js-debug](https://github.com/microsoft/vscode-js-debug) | Node entry files and explicit direct-execution guards |
| React/Vite | vscode-js-debug's Chrome profile | Vite configurations, using the configured or default dev-server port |

Install Delve and debugpy directly:

```bash
go install github.com/go-delve/delve/cmd/dlv@latest
python -m pip install debugpy
```

Keep `dlv`, `codelldb`, and `netcoredbg` on `PATH`. For Python, Wingman uses a
`debugpy-adapter` from `PATH` or a project virtual environment, and can also
reuse the adapter bundled by an installed `ms-python.debugpy` extension.
Wingman checks common user tool directories, Mason installs, and compatible
VS Code/Cursor extension directories. Java requires `jdtls` plus the java-debug
plug-in JAR; installed VS Code/Cursor and Mason bundles are detected
automatically, or set `WINGMAN_JAVA_DEBUG_BUNDLE` to the JAR. For JavaScript,
use a vscode-js-debug standalone release, compatible editor bundle containing
`dapDebugServer.js`, or Mason installation; `WINGMAN_JS_DEBUG_SERVER` can point
directly to `dapDebugServer.js`, and `WINGMAN_JS_DEBUG_ADAPTER` can name a
compatible wrapper executable.

Rust target names, kinds, and output directories come from Cargo's
machine-readable metadata. Rust and C# launch plans use existing debug build
output and otherwise show the expected executable path; run `cargo build` or
`dotnet build` before launching an unbuilt sample. TypeScript entry files run on
Node using a project-local `tsx` executable when present. React/Vite browser
plans launch Chrome at the configured port (5173 by default), so start the Vite
dev server first. Runnable samples for every adapter live in
[`examples/debug`](examples/debug).

Supported entry points receive CodeLens actions. During a session, click
Monaco's glyph margin to toggle source breakpoints. The Debug details pane shows
recursively expandable variables above the call stack. Polling preserves
expanded variable rows until debugger state actually changes. Closing the Debug
tab stops the active session. Adapter-defined launch arguments remain available
as documented JSON under **Adapter options**.

Every reviewed plan also chooses where program I/O runs. **Debug output**
captures ordinary program output in the Debug tab. **Terminal** starts
compatible adapters in Wingman's PTY and opens the terminal view inside that
same tab for stdin, ANSI output, resizing, and full-screen CLI/TUI programs.
This is a generic DAP host policy rather than a Go detector rule.
Adapter descriptors declare how they support it; the client also implements DAP
`runInTerminal` for future adapters that request a client-owned terminal. When
the debuggee exits, Wingman tears down the adapter cleanly and switches back to
the retained Debug output.

The protocol session and transports are language-neutral. Each language adapter
owns its source-target detector, deterministic launch policy, and DAP endpoint
descriptor. The generic DAP client validates paths, connects to or starts the
declared endpoint, and manages protocol state. Another language can therefore
be added as one cohesive adapter without changing the session client.

## 🛠️ Built-in Tools

Wingman comes with powerful built-in tools:

| Tool | Description |
|------|-------------|
| `read` | Read file contents with optional line range |
| `write` | Create or overwrite files |
| `edit` | Make surgical edits to existing files |
| `glob` | Find files using glob patterns |
| `grep` | Search file contents using regex patterns |
| `shell` | Execute shell commands |
| `agent` | Launch a sub-agent to handle independent tasks in a separate context |
| `schedule_task` | Schedule recurring or one-time work (interval, cron, or timestamp) that wakes the agent when due |
| `lsp` | Code intelligence (definitions, references, diagnostics, symbols, call hierarchy) |

### LSP Support

Wingman automatically detects and connects to language servers based on project files. No configuration needed — if you have a language server installed, Wingman will use it.

| Language | Server | Detected By |
|----------|--------|-------------|
| Go | `gopls` | `go.mod`, `go.work` |
| TypeScript/JS | `typescript-language-server`, `vtsls` | `tsconfig.json`, `package.json` |
| Deno | `deno lsp` | `deno.json`, `deno.jsonc` |
| Python | `basedpyright`, `pyright`, `pylsp`, `jedi-language-server` | `pyproject.toml`, `requirements.txt` |
| Rust | `rust-analyzer` | `Cargo.toml` |
| C/C++ | `clangd`, `ccls` | `compile_commands.json`, `CMakeLists.txt` |
| Java | `jdtls` | `pom.xml`, `build.gradle` |
| C# | `omnisharp`, `csharp-ls` | `*.csproj`, `*.sln` |
| F# | `fsautocomplete` | `*.fsproj`, `*.sln` |
| Ruby | `ruby-lsp`, `solargraph` | `Gemfile` |
| PHP | `intelephense`, `phpactor` | `composer.json` |
| Swift | `sourcekit-lsp` | `Package.swift` |
| Kotlin | `kotlin-language-server` | `build.gradle.kts` |
| Scala | `metals` | `build.sbt` |
| Dart | `dart language-server` | `pubspec.yaml` |
| Zig | `zls` | `build.zig` |
| Lua | `lua-language-server` | `.luarc.json` |
| Elixir | `elixir-ls`, `lexical` | `mix.exs` |
| Haskell | `haskell-language-server` | `stack.yaml`, `*.cabal` |
| OCaml | `ocamllsp` | `dune-project` |
| Clojure | `clojure-lsp` | `deps.edn`, `project.clj` |
| Gleam | `gleam lsp` | `gleam.toml` |
| Nix | `nixd` | `flake.nix`, `default.nix` |
| Vue | `vue-language-server` | `package.json` |
| Svelte | `svelteserver` | `package.json` |
| Astro | `astro-ls` | `package.json` |
| Bash | `bash-language-server` | `.bashrc`, `*.sh` |
| Terraform | `terraform-ls` | `main.tf`, `.terraform` |
| YAML | `yaml-language-server` | `.yamllint`, `docker-compose.yml` |
| Docker | `docker-langserver` | `Dockerfile` |
| Prisma | `prisma language-server` | `schema.prisma` |
| Typst | `tinymist` | `typst.toml` |
| LaTeX | `texlab` | `.latexmkrc` |

The LSP tool provides these operations:
- **diagnostics** / **workspaceDiagnostics** — Compiler errors and warnings
- **definition** / **implementation** — Navigate to symbol definitions or interface implementations
- **references** — Find all usages of a symbol
- **hover** — Type information and documentation
- **documentSymbol** / **workspaceSymbol** — List or search symbols
- **prepareCallHierarchy** / **incomingCalls** / **outgoingCalls** — Explore call graphs

## 🎨 Modes

- **Agent Mode** — Full autonomous operation with tool execution
- **Plan Mode** — Planning and analysis without project source edits

Toggle between modes using `Tab` or the explicit `/plan` and `/agent` commands.

## ⌨️ Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `Enter` | Send message |
| `Ctrl+J` | Insert a new line |
| `Ctrl+P` | Open the searchable command center |
| `Tab` | Toggle Agent/Plan mode (or autocomplete slash commands) |
| `@` | Open fuzzy file picker to add file context |
| `Ctrl+V` | Paste image or text directly from the system clipboard |
| `Cmd+V` / `Ctrl+Shift+V` | Paste text using the terminal's native shortcut |
| `Ctrl+O` | Open the searchable transcript inspector |
| `Ctrl+Y` | Copy the complete last assistant response to clipboard |
| `Ctrl+L` | Clear chat history |
| `Escape` | Cancel stream, close modal, or clear input |
| `Ctrl+C` | Cancel stream or clear input; press twice to exit |

## 📝 Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands and skills |
| `/model` | Select AI model and reasoning effort from available options |
| `/plan` | Enter planning mode |
| `/agent` | Return to execution mode |
| `/problems` | Show LSP diagnostics for the workspace |
| `/diff` | Show working tree changes |
| `/resume` | Resume the most recent saved session |
| `/clear` | Clear chat history |
| `/quit` | Exit application |

Skill slash commands (e.g. `/commit`, `/code-review`) also appear here — see **Skills** below.

## 🔧 Skills

Skills are reusable, invocable workflows defined in `SKILL.md` files. Project skills override personal, plugin, and bundled skills with the same name. Within each scope, the first listed directory wins:

**Project skills** (scoped to the current repo):
- `.wingman/skills/<name>/SKILL.md`
- `.agents/skills/<name>/SKILL.md`
- `.claude/skills/<name>/SKILL.md`

**Personal skills** (user-wide, across all projects):
- `~/.wingman/skills/<name>/SKILL.md`
- `~/.agents/skills/<name>/SKILL.md`
- `~/.claude/skills/<name>/SKILL.md`

This allows project-specific customization while keeping personal defaults reusable across repositories. Wingman uses the same directory order and the same first-wins rule for skills, [plugins](#-plugins), and [custom agents](#-custom-agents), and reports every shadowed entry on startup so a silently ignored file is visible.

Skill frontmatter follows the [Agent Skills specification](https://agentskills.io/specification): `name` and `description` are required; `license`, `compatibility`, `metadata`, and experimental `allowed-tools` are accepted. `allowed-tools` is descriptive metadata and never bypasses Wingman's normal tool approval policy.

### Bundled Skills

Wingman ships with built-in skills that are available immediately via slash commands. Their complete directories are copied to a managed temporary workspace snapshot, while a personal or project skill with the same name cleanly overrides the built-in:

Existing `~/.wingman/skills/<name>` customizations from older Wingman versions remain personal overrides. Rename or remove one only when you want the current bundled skill to take precedence.

| Skill | Description |
|-------|-------------|
| `/init` | Scan the project and generate an `AGENTS.md` with conventions and build commands |
| `/architecture` | Design or evaluate a code-grounded architecture and implementation blueprint |
| `/feature-dev` | Explore, design, implement, and verify non-trivial feature work |
| `/debug` | Reproduce, isolate, and diagnose unexpected behavior before fixing it |
| `/test` | Design, add, repair, or run focused behavioral tests |
| `/commit` | Stage and commit changes with a well-crafted commit message |
| `/pull-request` | Prepare, push, create, or update a reviewable pull request |
| `/skill-creator` | Create or improve a focused skill with optional scripts and resources |
| `/code-review` | Review code changes for correctness, style, and security |
| `/simplify` | Review changed code for reuse, quality, and efficiency, then fix issues |
| `/security-review` | Concise read-only security audit using parallel sub-agents |
| `/vuln-scan` | Static vulnerability scan that writes `VULN-FINDINGS.json` / `.md` |
| `/triage` | Verify, deduplicate, rank, and route raw security findings |
| `/patch` | Fix verified security findings and prove the remediation |
| `/threat-model` | Map assets, entry points, trust boundaries, and top threats |
| `/memory` | Save or revise durable user, feedback, project, and reference context |

### Custom Skill Example

```markdown
---
name: run-tests
description: Run the project test suite with coverage
---

# Testing Skill

Run tests with: `go test -cover ./...`
```

Place this file at `.wingman/skills/run-tests/SKILL.md` and invoke it with `/run-tests`.
Wingman supports Claude-style skill arguments: `$ARGUMENTS`, zero-based `$ARGUMENTS[N]`, and `$N`; non-empty arguments are appended as `ARGUMENTS: <value>` when no argument placeholder is present. The optional Claude extensions `arguments` (a space-separated string or string list) and `argument-hint` enable named positional substitutions such as `$component` and display `/skill [component]` hints in the UI. `${SKILL_DIR}` and `${PROJECT_DIR}` provide neutral directory variables, with `${CLAUDE_SKILL_DIR}` and `${CLAUDE_PROJECT_DIR}` accepted as compatibility aliases. The two extra frontmatter fields are intentionally non-portable and therefore rejected in Agent Plugin skills.

A skill may keep any supporting files next to `SKILL.md`. Common conventions are `references/` for selectively loaded guidance, `assets/` for files to copy or transform, `templates/` and `examples/` for reusable inputs, and `scripts/` for deterministic helpers. Wingman copies the complete directory tree for built-in skills, including dotfiles and underscore-prefixed resources.

Scripts run through Wingman's normal shell command path and its approval policy. Wingman does not infer dependencies or automatically create a virtual environment, so a skill that needs Python packages should document a reproducible command such as `uv run`, a lockfile-backed environment, or explicit venv setup.

A ready-to-copy resource-backed skill with metadata, named arguments, directory variables, a reference, and a helper script lives in [`examples/skills/run-tests`](examples/skills/run-tests).

## 🪝 Hooks

Wingman loads Codex-format hooks from `~/.codex/hooks.json`, `<project>/.codex/hooks.json`, and enabled plugins. It supports the Codex lifecycle events and JSON input/output decision protocol; matching command hooks run concurrently. Project and plugin hooks require a one-time confirmation in each session before their first matching command runs.

Command handlers are Codex-compatible. Wingman additionally supports Claude-style `type: "http"` handlers using the same JSON request and response body as command hooks. The HTTP extension requires explicit `allowedEnvVars` for environment interpolation in headers.

See the [Codex hooks reference](https://learn.chatgpt.com/docs/hooks) for the configuration shape. Wingman intentionally does not load the former Wingman-specific flat hook format.

## 🧩 Plugins

A plugin bundles skills, MCP servers, and optional lifecycle hooks. Wingman implements the portable [Agent Plugins specification](https://agent-plugins.org) v1.0.0.

Drop a plugin directory into any of these, project before personal, first name wins:

- `.wingman/plugins/<name>/`, `.agents/plugins/<name>/`, `.claude/plugins/<name>/` (project)
- `~/.wingman/plugins/<name>/`, `~/.agents/plugins/<name>/`, `~/.claude/plugins/<name>/` (personal)

A portable Agent Plugin starts with a `plugin.json` manifest and two standard component types. A complete package can also carry resources and client-owned extensions such as hooks:

```text
release-tools/
├── plugin.json                        # required manifest
├── mcp.json                           # MCP servers this plugin provides
├── hooks/
│   └── hooks.json                     # declared through extensions.com.openai
├── scripts/
│   └── release-context.sh             # hook helper
├── templates/
│   └── changelog.md                   # shared plugin resource
├── skills/
│   └── release-check/
│       ├── SKILL.md                   # one skill per immediate subdirectory
│       └── references/
│           └── release-policy.md      # skill-local resource
```

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "release-tools",
  "version": "1.0.0",
  "description": "Release checklist skill plus the MCP servers it needs"
}
```

`name` must be lowercase alphanumerics, hyphens, and periods. A manifest that violates the schema is rejected and reported. As explicit non-fatal exceptions, unknown top-level fields and a non-object `extensions` value are reported and ignored; unknown reverse-domain extension namespaces remain opaque.

Only `plugin.json` is a plugin manifest. Native `.codex-plugin`, `.claude-plugin`, and `.cursor-plugin` manifests are not loaded. Skills are read only from immediate subdirectories of `skills/`, and MCP servers only from `mcp.json`, as defined by the portable specification. Every loaded `SKILL.md` is validated against the Agent Skills name, directory-name, description, compatibility, and frontmatter constraints.

### Plugin hooks

A plugin can supply a Codex hook declaration under the client-owned `extensions.com.openai.hooks` field. Wingman accepts a single `./` path, an array of paths, an inline hooks object, or an array of inline objects. Manifest paths must remain inside the resolved plugin root. `com.openai` is a namespaced Agent Plugins extension defined by Codex, not a portable Agent Plugins core field; no hook file is loaded implicitly.

Plugin hook commands receive `PLUGIN_ROOT` and `PLUGIN_DATA`. The plugin data directory is created before hooks run. Plugin hooks are untrusted code: Wingman asks once per session before the first matching hook from each plugin executes.

### Plugin MCP servers

`mcp.json` uses the portable Agent Plugins format, which is stricter than Wingman's own `mcp.json`: every server declares its `type` explicitly, and `command` must be a bare executable name or a `./` path inside the plugin.

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "changelog": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@example/changelog-mcp", "--cache", "${PLUGIN_DATA}/cache"],
      "env": { "CHANGELOG_TEMPLATE": "${PLUGIN_ROOT}/templates/changelog.md" },
      "cwd": "${PLUGIN_DATA}"
    },
    "release-api": {
      "type": "streamable-http",
      "url": "https://releases.example.com/mcp"
    }
  }
}
```

Two variables are expanded in `args`, `env` values, and `cwd`, and are set in every plugin subprocess:

| Variable | Points at | Use for |
|----------|-----------|---------|
| `PLUGIN_ROOT` | the plugin directory | bundled scripts, binaries, templates |
| `PLUGIN_DATA` | `~/.wingman/plugin-data/<name>` (personal) or the project's state directory | caches, installed dependencies, generated files — survives plugin updates |

Remote servers must use HTTPS unless they point at loopback, and must not carry credentials in `headers`, which are visible package data rather than a secret store.

### Precedence and conflicts

Plugin components join the same namespaces as your own configuration, and your configuration always wins:

- **Skills** — a plugin skill is invoked by its plain name (`/release-check`) and also as `<plugin>:<skill>` (`/release-tools:release-check`). If a personal or project skill takes the plain name, the qualified form still reaches the plugin's copy.
- **MCP servers** — plugin servers sit below `~/.wingman/mcp.json` and `./mcp.json`, so a server you configure yourself replaces the plugin's entry of the same name.
- **Duplicate endpoints** — servers configured identically across any source are collapsed to one connection, so the same tools are not offered twice under different names. Any difference in headers, arguments, or environment keeps both.

Component failures never take down the rest of a plugin: an unreadable `mcp.json` still leaves its skills loaded, and one invalid server entry skips only itself. Everything skipped is reported on startup.

A ready-to-copy plugin lives in [`examples/plugins/release-tools`](examples/plugins/release-tools).

## 🤝 Custom Agents

Custom agent types extend the built-in sub-agent roster (`explore`, `code-reviewer`, `verification`, …) with your own specialists. Wingman discovers them from markdown files (first definition of a name wins, project before personal):

- `.wingman/agents/*.md`, `.agents/agents/*.md`, `.claude/agents/*.md` (project)
- `~/.wingman/agents/*.md`, `~/.agents/agents/*.md`, `~/.claude/agents/*.md` (personal)

```markdown
---
name: db-expert
description: Deep PostgreSQL schema and query analysis
access: read-only
---

You are a PostgreSQL specialist. Inspect schemas, migrations, and queries...
```

The body becomes the agent's system prompt. `access` selects the toolset — `read-only` (search/read only), `verify` (read plus build/test commands), or `all` (default) — and an optional `model: plan` or `model: utility` picks the session's planning or utility model instead of inheriting. A custom definition with a built-in name replaces that built-in.

## 🖥️ Server Mode

Wingman includes a web-based UI server — useful for IDE integrations or browser-based access:

```bash
wingman server [--port 9000]
```

This starts an HTTP server at `http://localhost:9000` (or another available
port) with a React UI featuring a chat panel, file browser, diff viewer,
diagnostics panel, a terminal (multiple
xterm.js sessions, shell of your choice, `Ctrl+Alt+T`), and session management.
`Ctrl+P` opens the command palette — same shortcut as the TUI command center
(`Cmd/Ctrl+K` works too). The server uses WebSockets for real-time streaming.

## 🧩 CLI Wrappers

Wingman can launch other coding agents pre-configured to use a Wingman backend:

```bash
wingman run claude [args...]
wingman run claude-desktop [args...]
wingman run codex [args...]
wingman run copilot [args...]
wingman run gemini [args...]
wingman run goose [args...]
wingman run junie [args...]
wingman run opencode [args...]
wingman run pi [args...]
```

Each wrapper automatically configures the target CLI with the correct endpoint
and authentication. `WINGMAN_URL` is required. These wrappers are deliberately
Wingman-backed and are separate from the native subscription-backed
`wingman --agent <name>` modes.

The Codex wrapper also supplies an embedded model catalog filtered to the
OpenAI models advertised by the Wingman backend, so Codex's in-app `/model`
selector remains available with the correct model-specific instructions and
tool metadata.

## 📊 Terminal-Bench

Wingman can run Terminal-Bench tasks through Harbor's generic ACP agent runner.
The integration installs the released Wingman binary inside each task container,
preserving the task's own Docker environment and verifier. See the
[Terminal-Bench compatibility guide](bench/tbench/README.md) for the pinned
agent descriptor and benchmark commands.
