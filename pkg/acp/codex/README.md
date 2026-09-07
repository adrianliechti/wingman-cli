# codex

ACP bridge for the Codex CLI. `Spawn` launches `codex app-server` and translates
its JSON-RPC notifications into ACP session updates.

## Protocol reference

[Codex App Server](https://learn.chatgpt.com/docs/app-server)

The app-server surface is what this package speaks: `initialize`, `thread/start`,
`turn/start`, and the `item/*` and `turn/*` notification streams handled in
`events.go`.

## ACP versions

This adapter implements **ACP v1** using `acp-go-sdk v0.13.5`. A client requesting
v2 receives `protocolVersion: 1` and can continue with v1 or disconnect, as required
by [version negotiation](https://agentclientprotocol.com/protocol/v1/initialization).
Codex's App Server API also has types named `v2`; that is a separate protocol and
does not indicate ACP v2 support.

The [ACP v2 draft](https://agentclientprotocol.com/protocol/v2/migration) was
reviewed on 2026-09-07. It needs more than a version-number change:

| Surface | Implemented v1 | Draft v2 |
| --- | --- | --- |
| Prompt completion | Pending request returns `stopReason` | Immediate acknowledgement; `state_update` reports completion |
| Initialization | `agentInfo`, `agentCapabilities` | `info`, `capabilities` with a different structure |
| Tools and permissions | `tool_call`, `tool_call_update`, permission `toolCall` | Tool upserts, permission `title` and `subject` |
| History | `session/load` replays history | `session/resume` with `replayFrom` |

Keep one implementation while the Go SDK targets v1. Native v2 support should
migrate the connection surface together with these lifecycle changes when SDK
support is available. The wire tests exercise both a v1 initialization and a v2
initialization falling back to v1, including both cancellation methods.

## Connection reliability

Each turn has an ordered, bounded event queue. The app-server reader only routes
events, so a client update or permission dialog cannot block RPC replies. Events
and approval requests with a turn ID must match the active turn, including events
that arrive before the `turn/start` reply. Replaced sessions cannot remove the
replacement's handlers during cancellation cleanup.

Command output is buffered and sent once when the command completes. Each command
retains at most the last 1 MiB, with a visible truncation marker. The same limit
applies to command output replayed from history. This follows the scratch Codex
adapter's fixes for quadratic output growth (`4e5cffb`, `8aef91b`).

RPC acknowledgements have a 30-second deadline; interrupts have a 2-second
deadline. A timed-out start or interrupt retires the backend connection because
the adapter cannot establish which work is still running. Stalled client writes fail after
10 seconds and close the stdio output. These limits apply to transport operations;
model turns and user approval waits have no fixed overall deadline. Backend EOF,
framing errors, malformed completion payloads, exhausted retries, and failed turns become errors. Retrying error
events keep the turn open, and already-received completion events survive EOF.
Process exit is also monitored independently of stdout, so a tool that inherits
the pipe cannot keep a dead app-server session pending.

Cancellation releases active and cancelled queued prompts. Late permission
responses cannot approve cancelled work, and steering leaves an open permission
request intact. These checks apply the lifecycle lessons from the reference
Codex cancellation fix (`f67ca5f`) and Claude's exact result attribution and
JetBrains approval/steering fixes (`a04d354`, `8710ce1`). The Codex app-server's
`turn/steer` API already queues input without Claude's interrupting `now` delivery.
The next prompt waits for cancellation cleanup, even when the previous start
reply arrived after cancellation. Resolving a backend approval cancels only that
request's dialog, using both its request ID and thread ID.

URL elicitation IDs are generated per interaction. Accepting a URL request means
consent to open it; `serverRequest/resolved` does not prove that the external
workflow finished. The adapter therefore omits the optional `elicitation/complete`
notification instead of completing unrelated URL interactions. This follows the
[elicitation lifecycle](https://agentclientprotocol.com/protocol/v1/elicitation).
The pinned Go SDK still lacks top-level elicitation scope fields; session scope
is currently carried in `_meta.sessionId`. Standard permission requests carry
their normal top-level `sessionId`.

`reliability_test.go`, `rpc_test.go`, `turn_stream_test.go`, and
`approval_lifecycle_test.go` cover these cases with local transports and a helper
process. `protocol_review_test.go` and `cancellation_order_test.go` verify protocol
envelopes and cancellation ordering. These tests do not make model requests or
replace an IntelliJ integration test.

## History and reference implementations

`session/load` reads all history before reporting success. Paginated Codex stores
use `thread/turns/list` with full items, preserve chronological replay order, and
reject repeated cursors. Legacy stores retain `thread/read` with `includeTurns`.
Read failures are reported instead of silently returning an empty conversation.
Resume and fork requests omit unnecessary history hydration. These changes follow
the current adapter's [history pagination fix](https://github.com/agentclientprotocol/codex-acp/commit/1a3c01e).
`history_review_test.go` covers both store formats and failure cases.

The review compared the local scratch Codex adapter at `296069e`, the local
Claude adapter at `3e23c5b`, and the current
[App Server adapter](https://github.com/agentclientprotocol/codex-acp) at `1a3c01e`.
The scratch Codex repository has moved development to that App Server adapter.

## Plans

This bridge currently does not forward `turn/plan/updated` task lists as ACP v1
`plan` notifications. A completed Codex `plan` item is surfaced as agent text and
offered to the client via `requestPlanImplementation`. Approving implementation
starts a separate backend turn with its own event and approval lifecycle.
