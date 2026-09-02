# claude

ACP bridge for the Claude Code CLI. The session spawns `claude` with
`--output-format stream-json --input-format stream-json` and translates the
newline-delimited envelopes into ACP session updates.

## Protocol reference

- https://code.claude.com/docs/en/headless — the stream-json protocol from a
  programmatic consumer's view: `system/init`, `system/api_retry`, subagent
  messages carrying `parent_tool_use_id`, and the terminating `result` message.
- https://code.claude.com/docs/en/cli-reference — the flags `cliArgsLocked`
  passes: `--output-format`, `--input-format`, `--resume`, `--session-id`,
  `--add-dir`, `--mcp-config`, `--model`, `--effort`.
- https://code.claude.com/docs/en/agent-sdk/typescript — the message type
  schemas (`SDKSystemMessage` and friends). This is the closest equivalent to
  Codex's app-server reference.

Note that `anthropic-sdk-typescript` documents the Anthropic **API** (Messages
API), not this stream — it is a different layer and not a reference for this
package.

## Plans

This bridge does not emit ACP `plan_update`. Current Claude Code exposes no
plan-emitting tool: the `TodoWrite` and `TaskCreate`/`TaskUpdate`/`TaskList`
tools that earlier versions surfaced are gone, so there is nothing to translate.
Plan *mode* is unrelated and still supported — see the `plan` entry in
`sessionModes` and the `ExitPlanMode` handling in `approvals.go`.
