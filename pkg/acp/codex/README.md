# codex

ACP bridge for the Codex CLI. `Spawn` launches `codex app-server` and translates
its JSON-RPC notifications into ACP session updates.

## Protocol reference

https://learn.chatgpt.com/docs/app-server

The app-server surface is what this package speaks: `initialize`, `thread/start`,
`turn/start`, and the `item/*` and `turn/*` notification streams handled in
`events.go`.

## Plans

This bridge does not emit ACP `plan_update`. Codex 0.152.1 produces no plan item
even when explicitly asked to use a plan tool, and `turn/plan/updated` is
documented as carrying empty item arrays — clients are directed to `item/*` as
the authoritative source. A `plan` item, if one ever arrives, is surfaced as
agent text and offered to the client via `requestPlanImplementation`.
