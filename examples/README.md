# Examples

Copy-pasteable starting points for Wingman's configuration files. Each file is
validated against the real parsers by `examples_test.go`, so they always match
the current release.

| Example | Copy to | Purpose |
|---------|---------|---------|
| [`mcp.json`](mcp.json) | `./mcp.json` (project) or `~/.wingman/mcp.json` (global) | Connect MCP servers, local or remote |
| [`agents/db-expert.md`](agents/db-expert.md) | `.wingman/agents/` or `.claude/agents/` (project), `~/.wingman/agents/` (personal) | Read-only custom sub-agent |
| [`agents/release-verifier.md`](agents/release-verifier.md) | same as above | Custom sub-agent that may run builds and tests |
| [`skills/run-tests/SKILL.md`](skills/run-tests/SKILL.md) | `.wingman/skills/run-tests/SKILL.md` | Parameterized skill invoked as `/run-tests` |

Project instructions need no template: put an `AGENTS.md` (or `CLAUDE.md`) with
plain markdown guidelines in the project root — see the main README.
