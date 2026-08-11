# Examples

Copy-pasteable starting points for Wingman's configuration files. The examples
are validated against the real parsers by `examples_test.go`, so their schemas
and the capabilities described below stay aligned with the current release.

## At a glance

| Example | Copy to | What it demonstrates |
|---------|---------|----------------------|
| [`mcp.json`](mcp.json) | `./mcp.json` or `~/.wingman/mcp.json` | Project/personal stdio and remote MCP servers |
| [`agents/db-expert.md`](agents/db-expert.md) | `.wingman/agents/` or `~/.wingman/agents/` | Read-only custom sub-agent |
| [`agents/release-verifier.md`](agents/release-verifier.md) | same as above | Verification sub-agent with a model preference |
| [`skills/run-tests/`](skills/run-tests) | `.wingman/skills/run-tests/` | A resource-backed skill with metadata, named arguments, path variables, and a helper script |
| [`plugins/release-tools/`](plugins/release-tools) | `.wingman/plugins/release-tools/` or `~/.wingman/plugins/release-tools/` | An Agent Plugin bundling a portable skill, three MCP transports, resources, and a lifecycle hook |
| [`non-interactive/`](non-interactive) | Run in place | A read-only `wingman exec` run with validated JSON Schema output |

Project instructions need no template: put an `AGENTS.md` (or `CLAUDE.md`) with
plain Markdown guidelines in the project root.

## Skill walkthrough

Copy the complete directory because resources live beside `SKILL.md`:

```sh
mkdir -p .wingman/skills
cp -R examples/skills/run-tests .wingman/skills/
```

Then invoke `/run-tests` for the whole repository or `/run-tests ./pkg/skill`
for one package. The sample intentionally exercises the full skill surface:

- portable Agent Skills fields (`license`, `compatibility`, string `metadata`,
  and experimental `allowed-tools`);
- Claude-compatible `arguments` and `argument-hint`, including the named
  `$package` substitution and the original `$ARGUMENTS` text;
- `${SKILL_DIR}` and `${PROJECT_DIR}` for stable access to bundled resources and
  the active project;
- a selectively loaded reference and a deterministic helper in `scripts/`.

The `arguments` and `argument-hint` fields are convenient Wingman/Claude
extensions. Remove them when publishing the skill inside a portable Agent
Plugin. `allowed-tools` documents intent; it never bypasses tool approval.

## Plugin walkthrough

Copy the plugin as one directory:

```sh
mkdir -p .wingman/plugins
cp -R examples/plugins/release-tools .wingman/plugins/
```

The plugin demonstrates the standard `plugin.json`, immediate `skills/`
children, and `mcp.json`, plus the Codex-owned `extensions.com.openai.hooks`
extension. Its hook is declared by a `./` path, receives `PLUGIN_ROOT` and
`PLUGIN_DATA`, and injects the plugin's resource locations when a session starts.
The MCP file includes stdio, streamable HTTP, and SSE entries; its stdio server
shows variable expansion in arguments, environment values, and the working
directory.

Invoke the bundled skill as `/release-check v1.2.3` or with its stable qualified
name, `/release-tools:release-check v1.2.3`. The qualified form continues to
reach this plugin if a project or personal skill shadows the bare name.

The package names and remote endpoints are illustrative placeholders. Replace
them before enabling the sample in a real project. Wingman asks for trust before
running a project or plugin hook for the first time in a session.
