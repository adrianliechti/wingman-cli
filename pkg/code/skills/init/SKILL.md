---
name: init
description: Generate an AGENTS.md file with project conventions, build commands, and guidelines.
when-to-use: When starting with a new project, or when the user wants to create or update their AGENTS.md.
---
# Initialize Project Guidelines

Scan the current project and generate an `AGENTS.md` file in the project root. This file tells the AI agent about project-specific conventions and commands.

## Steps

1. **Explore the project structure** -- use `find` and `ls` to understand the directory layout, key files, and technology stack.

2. **Detect the tech stack** -- look for:
   - Language: check file extensions, `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `pom.xml`, etc.
   - Framework: check imports, config files, directory conventions
   - Package manager: npm/yarn/pnpm/bun, pip/poetry/uv, cargo, etc.

3. **Find build & test commands** -- check:
   - `Makefile`, `justfile`, `Taskfile.yml`
   - `package.json` scripts
   - CI config (`.github/workflows/`, `.gitlab-ci.yml`)
   - Common patterns (`go build ./...`, `cargo build`, `npm run build`)

4. **Detect code style** -- check for:
   - Linter configs (`.eslintrc`, `.golangci.yml`, `rustfmt.toml`, `ruff.toml`)
   - Formatter configs (`.prettierrc`, `gofmt`, `biome.json`)
   - Editor config (`.editorconfig`)

5. **Check for existing AGENTS.md** -- if one exists, read it and suggest improvements rather than overwriting.

6. **Generate AGENTS.md** with these sections:

```markdown
# Project Guidelines

## Tech Stack
[Language, framework, key dependencies]

## Build & Run
[How to build, run, and test the project -- include exact commands]

## Code Style
[Formatting, linting, naming conventions]

## Project Structure
[Key directories and their purpose]

## Testing
[How to run tests, testing conventions, how to run a single test]

## Other
[Any other project-specific notes]
```

Keep it concise -- focus on information that ISN'T obvious from the code itself. Include exact commands that an agent can copy-paste. Omit sections that don't apply.
