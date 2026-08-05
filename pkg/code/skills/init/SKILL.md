---
name: init
description: "Create or refresh a concise AGENTS.md from verified repository evidence: instruction hierarchy, exact build and test commands, architecture boundaries, code conventions, and non-obvious operational constraints."
when-to-use: When onboarding Wingman to a repository, fixing stale project guidance, or capturing durable conventions that future coding sessions must follow.
---
# Initialize Project Guidelines

Create or refresh the root `AGENTS.md`. Preserve correct existing guidance, remove stale claims, and add only durable information that changes how an agent should work. Do not create nested instruction files unless the user asks.

## Steps

1. **Read the instruction hierarchy** -- find existing `AGENTS.md`, `CLAUDE.md`, and equivalent project guidance from the repository root to the current directory. Note conflicts and scope; do not silently overwrite useful rules.

2. **Map the project** -- use targeted file and symbol searches to identify:
   - Language: check file extensions, `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `pom.xml`, etc.
   - Framework: check imports, config files, directory conventions
   - Package manager: npm/yarn/pnpm/bun, pip/poetry/uv, cargo, etc.
   - Entrypoints, generated code, workspace/package boundaries, and high-risk directories

3. **Verify commands** -- derive exact commands from:
   - `Makefile`, `justfile`, `Taskfile.yml`
   - `package.json` scripts
   - CI config (`.github/workflows/`, `.gitlab-ci.yml`)
   - Existing contributor documentation

   Prefer commands the repository itself declares. Run a narrow, safe validation when practical; otherwise label the command as discovered but unverified. Never invent a conventional command merely because it fits the language.

4. **Extract non-obvious conventions** -- inspect:
   - Linter configs (`.eslintrc`, `.golangci.yml`, `rustfmt.toml`, `ruff.toml`)
   - Formatter configs (`.prettierrc`, `gofmt`, `biome.json`)
   - Editor config (`.editorconfig`)
   - Representative implementation and test files
   - Recent history for recurring workflow constraints, without copying ephemeral branch or issue details

   Record conventions only when supported by configuration, repeated code, or explicit documentation. Omit facts a competent agent can immediately infer from filenames.

5. **Write or update `AGENTS.md`** with the smallest useful set of sections:

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

## Constraints
[Generated files, compatibility surfaces, security boundaries, or workflow traps]
```

Keep it concise and imperative. Include copy-pasteable commands and scope them to the directory where they must run. Omit empty sections, generic advice, temporary task context, and duplicated README content. After writing, reread the result against the evidence and call out any command you could not verify.
