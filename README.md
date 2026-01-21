# Wingman CLI

A powerful AI-powered coding assistant that runs directly in your terminal. Wingman helps you with coding tasks by reading files, executing commands, editing code, and writing new files — all through natural conversation.

![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)
![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)

## ✨ Features

- **Interactive TUI** — Rich terminal interface with markdown rendering and syntax highlighting
- **File Operations** — Read, write, edit, and search files in your codebase
- **Shell Integration** — Execute shell commands with elicitation
- **MCP Support** — Extend functionality with Model Context Protocol servers
- **Context Management** — Automatic conversation compaction to handle long sessions
- **Multi-Model Support** — Works with any [OpenResponses API](https://www.openresponses.org) compatible endpoint with auto-selection
- **Rewind & Diff** — Checkpoint-based undo with visual diff viewer
- **Skills** — Define custom workflows using [Agent Skills](https://agentskills.io) format
- **Image Support** — Paste images from clipboard for vision-capable models
- **File Context** — Add files to context with `@filename` or `/file` command
- **Theme Detection** — Automatic light/dark theme based on terminal settings

## 📦 Installation

### From Source

```bash
go install github.com/adrianliechti/wingman-cli@latest
```

### Build Locally

```bash
git clone https://github.com/adrianliechti/wingman-cli.git
cd wingman-cli
go build -o wingman .
```

## 🚀 Quick Start

1. **Set up your API key:**

```bash
# For any OpenResponses API compatible endpoint
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

## ⚙️ Configuration

### Environment Variables

| Variable | Description |
|----------|-------------|
| `OPENAI_API_KEY` | OpenAI API key (required) |
| `OPENAI_BASE_URL` | Custom OpenAI-compatible API endpoint |
| `OPENAI_MODEL` | Model to use (auto-selected if not specified) |

**Alternative: Wingman Server**

| Variable | Description |
|----------|-------------|
| `WINGMAN_URL` | Wingman server URL (takes priority over OpenAI vars) |
| `WINGMAN_TOKEN` | Wingman authentication token |
| `WINGMAN_MODEL` | Model to use |

### Project Configuration

Create an `AGENTS.md` file in your project root to provide context-specific instructions:

```markdown
# Project Guidelines

- Use Go 1.25+ features
- Follow standard Go project layout
- Write tests for all new functionality
```

### MCP Integration

Add a `mcp.json` file to integrate with MCP servers:

```json
{
  "servers": {
    "my-server": {
      "command": "npx",
      "args": ["-y", "@my-org/my-mcp-server"]
    }
  }
}
```

## 🛠️ Built-in Tools

Wingman comes with powerful built-in tools:

| Tool | Description |
|------|-------------|
| `read` | Read file contents with optional line range |
| `write` | Create or overwrite files |
| `edit` | Make surgical edits to existing files |
| `ls` | List directory contents |
| `find` | Find files using glob patterns |
| `grep` | Search file contents using regex patterns |
| `shell` | Execute shell commands |

## 🎨 Modes

- **Agent Mode** — Full autonomous operation with tool execution
- **Plan Mode** — Planning and analysis without making changes

Toggle between modes using `Tab` key.

## ⌨️ Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `Enter` | Send message |
| `Tab` | Toggle Agent/Plan mode |
| `Shift+Tab` | Cycle through available models |
| `@` | Open file picker to add context |
| `Ctrl+V` / `Cmd+V` | Paste image from clipboard |
| `Ctrl+L` | Clear chat history |
| `Escape` | Clear input and pending attachments |
| `Ctrl+C` | Close modal or exit |

## 📝 Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/model` | Select AI model from available options |
| `/file` | Add file to context |
| `/paste` | Paste from clipboard |
| `/plan` | Show current plan |
| `/diff` | Show changes from session baseline |
| `/rewind` | Restore to previous checkpoint |
| `/clear` | Clear chat history |
| `/quit` | Exit application |

## 🔧 Skills

Skills are reusable workflows defined in `SKILL.md` files. Wingman discovers skills from:
- `.skills/`
- `.github/`
- `.claude/`
- `.opencode/`

Example skill file (`.skills/testing/SKILL.md`):

```markdown
---
name: run-tests
description: Run the project test suite with coverage
---

# Testing Skill

Run tests with: `go test -cover ./...`
```