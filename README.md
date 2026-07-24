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
- **MCP Support** — Extend functionality with Model Context Protocol servers
- **Multi-Model Support** — Works with any [OpenResponses API](https://www.openresponses.org) compatible endpoint with auto-selection
- **Rewind & Diff** — Checkpoint-based undo with visual diff viewer
- **Skills** — Define custom workflows using [Agent Skills](https://agentskills.io) format
- **Image Support** — Paste images from clipboard for vision-capable models
- **File Context** — Add files to context with `@` or drag-and-drop file paths
- **Theme Detection** — Automatic light/dark theme based on terminal settings
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

3. **Start chatting!** Ask Wingman to help with coding tasks:

```
> Show me all TODO comments in this project
> Refactor the config package to use dependency injection
> Write tests for the agent module
```

4. **Resume a previous session:**
```bash
wingman --resume              # resume the most recent session
wingman --resume <session-id> # resume a specific session
```

## ⚙️ Configuration

### Environment Variables

**Backend** — connect to a Wingman server, or any OpenAI-compatible API:

| Variable | Description |
|----------|-------------|
| `WINGMAN_URL` | Wingman server URL (takes priority over the OpenAI variables) |
| `WINGMAN_TOKEN` | Wingman server authentication token |
| `OPENAI_API_KEY` | API key for an OpenAI-compatible backend |
| `OPENAI_BASE_URL` | OpenAI-compatible API endpoint (default: `https://api.openai.com/v1`) |

**Models & Reasoning** — every value is optional; unset values are chosen automatically by role (plan → largest available model, code → medium, utilities → smallest):

| Variable | Description |
|----------|-------------|
| `WINGMAN_MODEL` | Coding model; takes priority over `OPENAI_DEFAULT_MODEL` |
| `WINGMAN_MODEL_PLAN` | Plan-mode model (default: largest available, e.g. Opus/Sol) |
| `WINGMAN_MODEL_UTILITY` | Model for recaps and compaction summaries (default: smallest available, e.g. Haiku/Luna) |
| `WINGMAN_EFFORT` | Coding reasoning effort: `none`/`low`/`medium`/`high`/`xhigh`/`max` (default: `high`) |
| `WINGMAN_EFFORT_PLAN` | Plan-mode reasoning effort (default: `xhigh` on large models, else `high`) |
| `WINGMAN_LARGE_CONTEXT` | `1` compacts against the model's full context window instead of stopping at the provider's long-context price threshold |

**Behavior**

| Variable | Description |
|----------|-------------|
| `WINGMAN_SANDBOX` | `off` lifts the workspace path restriction from the file tools |
| `WINGMAN_ELICITATION` | Headless (ACP) sessions: `accept` or `cancel` answers elicitation prompts automatically |
| `WINGMAN_<AGENT>_PATH` | Path override for an external agent binary (e.g. `WINGMAN_CODEX_PATH`) |

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
| `Tab` | Toggle Agent/Plan mode (or autocomplete slash commands) |
| `Shift+Tab` | Cycle through available models |
| `@` | Open fuzzy file picker to add file context |
| `Ctrl+V` | Paste image or text directly from the system clipboard |
| `Cmd+V` / `Ctrl+Shift+V` | Paste text using the terminal's native shortcut |
| `Ctrl+O` | Open the full transcript |
| `Ctrl+Y` | Copy last assistant response to clipboard |
| `Ctrl+L` | Clear chat history |
| `Escape` | Cancel stream, close modal, or clear input |
| `Ctrl+C` | Cancel stream or clear input; press twice to exit |

## 📝 Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands and skills |
| `/model` | Select AI model from available options |
| `/effort` | Set reasoning effort (`auto`, `low`, `medium`, `high`) |
| `/plan` | Enter planning mode |
| `/agent` | Return to execution mode |
| `/problems` | Show LSP diagnostics for the workspace |
| `/diff` | Show changes from session baseline (requires git) |
| `/rewind` | Restore to a previous checkpoint (requires git) |
| `/resume` | Resume the most recent saved session |
| `/clear` | Clear chat history |
| `/quit` | Exit application |

Skill slash commands (e.g. `/commit`, `/code-review`) also appear here — see **Skills** below.

## 🔧 Skills

Skills are reusable, invocable workflows defined in `SKILL.md` files. Wingman discovers skills from these locations (later directories take priority):

**Personal skills** (user-wide, across all projects):
- `~/.agents/skills/<name>/SKILL.md`
- `~/.wingman/skills/<name>/SKILL.md`
- `~/.claude/skills/<name>/SKILL.md`
- `~/.config/opencode/skills/<name>/SKILL.md`

**Project skills** (scoped to the current repo):
- `.agents/skills/<name>/SKILL.md`
- `.wingman/skills/<name>/SKILL.md`
- `.claude/skills/<name>/SKILL.md`
- `.opencode/skills/<name>/SKILL.md`

Project skills override personal skills with the same name, allowing per-project customization.

### Bundled Skills

Wingman ships with built-in skills that are available immediately via slash commands and are materialized to `~/.wingman/skills/` on first use so you can customize them:

| Skill | Description |
|-------|-------------|
| `/init` | Scan the project and generate an `AGENTS.md` with conventions and build commands |
| `/feature-dev` | Explore, design, implement, and verify non-trivial feature work |
| `/commit` | Stage and commit changes with a well-crafted commit message |
| `/code-review` | Review code changes for correctness, style, and security |
| `/security-review` | Concise read-only security audit using parallel sub-agents |
| `/vuln-scan` | Static vulnerability scan that writes `VULN-FINDINGS.json` / `.md` |
| `/triage` | Verify, deduplicate, rank, and route raw security findings |
| `/patch` | Fix verified security findings and prove the remediation |
| `/threat-model` | Map assets, entry points, trust boundaries, and top threats |
| `/simplify` | Review changed code for reuse, quality, and efficiency, then fix issues |

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

Skills support argument placeholders (`${ARGUMENTS}`, `${1}`, named args) for parameterized workflows.

## 🖥️ Server Mode

Wingman includes a web-based UI server — useful for IDE integrations or browser-based access:

```bash
wingman server [--port 4242]
```

This starts an HTTP server at `http://localhost:4242` with a React UI featuring a chat panel, file browser, diff viewer, checkpoint browser, diagnostics panel, and session management. The server uses WebSockets for real-time streaming.

## 🔀 Proxy Mode

When `WINGMAN_URL` is set, Wingman can act as a local API proxy with a TUI dashboard for inspecting requests:

```bash
wingman proxy [--port 4242]
```

This starts a local OpenAI-compatible proxy server that forwards requests to your Wingman server, showing real-time request/response details in a terminal UI.

## 🧩 CLI Wrappers

When `WINGMAN_URL` is set, Wingman can launch other coding agents pre-configured to use your Wingman server as their backend:

```bash
wingman codex [args...]    # Launch OpenAI Codex CLI
wingman claude [args...]   # Launch Claude Code
wingman copilot [args...]  # Launch GitHub Copilot CLI
wingman gemini [args...]   # Launch Gemini CLI
wingman opencode [args...] # Launch OpenCode
```

Each wrapper automatically configures the target CLI tool with the correct endpoint and authentication.

## 🤖 Claw Mode

Wingman includes an experimental multi-agent orchestration mode:

```bash
wingman claw
```

Claw manages a pool of named agents with persistent memory, scheduled tasks, and a TUI interface. Each agent has its own sandboxed workspace and can spawn sub-agents. Agents persist their sessions across restarts and support proactive check-in schedules.

## 📊 Terminal-Bench

Wingman can run Terminal-Bench tasks through Harbor's generic ACP agent runner.
The integration installs the released Wingman binary inside each task container,
preserving the task's own Docker environment and verifier. See the
[Terminal-Bench compatibility guide](bench/tbench/README.md) for the pinned
agent descriptor and benchmark commands.
