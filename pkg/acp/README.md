# ACP integration

`pkg/code/acp` hosts ACP agents as a Wingman client. `pkg/acp/server` exposes
Wingman to ACP clients. The `claude`, `codex`, and `pi` packages bridge their
respective backends to the same ACP interface.

The integration supports ACP protocol version 1. Major versions are negotiated
by explicit support, not by assuming that every lower number is compatible.
See the official [initialization](https://agentclientprotocol.com/protocol/v1/initialization)
and [cancellation](https://agentclientprotocol.com/protocol/v1/cancellation) rules.

## Connection and turn lifetime

- Owned connections use one shared `ConnectionWriter`. Writes time out after
  ten seconds; failed or partial frames retire the transport.
- The client keeps a cancelled prompt's original RPC alive until its response
  drains preceding notifications. The session remains busy during cleanup. If
  the peer does not settle within two seconds after the cancellation attempt,
  the connection is closed so its output cannot enter a later turn.
- A full client update buffer fails the affected turn with an explicit error.
  It does not block the SDK notification worker and other sessions' replies.
  Host state-change callbacks also run outside that worker and are coalesced.
- Emitted content owns its mutable data. Accumulating reasoning history cannot
  change a delta already delivered to the UI.
- Permission and form requests inherit their session's turn cancellation.
  Late answers cannot authorize cancelled work or be reassigned to another
  session. Form scope is carried in `_meta.sessionId` while the Go SDK lacks
  a top-level field for it.
- Claude stamps prompt UUIDs and matches result UUIDs when the CLI supplies
  them. Legacy results without UUIDs still use the serialized turn lifecycle.
  Pi waits for `agent_settled` after abort acknowledgement; an unresponsive
  cancellation closes that session's backend.
- The Wingman server retains workspace resources while a prompt is active,
  rejects session attachments during shutdown, and reports failed history
  replay writes instead of returning a successful load response.

## Verification

Run the protocol contracts and race tests with:

```sh
go test -race ./pkg/acp/... ./pkg/code/acp ./pkg/code/agents ./cmd/wingman -count=1 -timeout=120s
```

The lifecycle review tests use deterministic scheduling (`testing/synctest`),
in-memory protocol connections, and subprocess fixtures. Cases include unread
streams, concurrent sessions, delayed approvals and cancellation completion,
duplicate results, EOF immediately after completion, inherited stdout, partial
writes, reentrant callbacks, and concurrent session closure. These tests do not
replace a live IntelliJ integration run.
