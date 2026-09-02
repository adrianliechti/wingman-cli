# ACP coding-flow benchmark

This benchmark drives an agent exclusively through ACP and sends telemetry from
each harness to a small in-process OTLP/HTTP receiver. Its `dashboard` scenario
uses one disposable repository for two turns:

1. Build a Vite + React + TypeScript + Tailwind finance dashboard.
2. Inspect that repository and add a functional 7D / 30D / 90D chart selector.

Each turn must pass `npm run build` plus lightweight source requirements. The
run JSON contains wall time, ACP events, outcome, and ACP-reported usage. Its
`.otel.json` sidecar contains the received trace, metric, and log exports plus a
small index of signal, service, span, metric, and event names. Installed
dependencies are symlinked from `server/ui/node_modules`, so agents are
instructed not to use the network or alter dependency versions.

Build the exact Wingman revision being measured:

```bash
go build -o /private/tmp/wingman-acpflow ./cmd/wingman
```

Run one case against a local gateway:

```bash
go run ./bench/acpflow \
  -binary /private/tmp/wingman-acpflow \
  -gateway http://127.0.0.1:4242 \
  -scenario dashboard \
  -agent wingman \
  -model gpt-5.6-sol \
  -effort high \
  -output /private/tmp/wingman-sol-dashboard.json
```

The command above also writes
`/private/tmp/wingman-sol-dashboard.otel.json`. Use `-telemetry-output` to choose
a different sidecar path. When `-output` is set, the benchmark also rebuilds
`acpflow-<effort>-report.json` and a self-contained
`acpflow-<effort>-report.html` presentation from every matching result/sidecar
pair in that directory. Pass `-report none` to disable this, or `-report PATH`
to choose another HTML path.

The report can be regenerated from existing captures without making model
requests:

```bash
go run ./bench/acpflow \
  -report-only /private/tmp/acpflow-results \
  -effort high \
  -report /private/tmp/acpflow-results/comparison.html
```

The HTML embeds a content-free normalized copy of the comparison data, so it
opens directly from disk and prints cleanly to PDF. The adjacent report JSON is
useful for further analysis. Links in the final slide open the raw run and OTEL
artifacts beside the report.

OTLP protobuf and JSON payloads are accepted and stored as compact canonical
JSON. Content and identity attributes are removed; Codex's per-SSE-frame logs
are omitted because its request, tool, timing, and token signals already cover
the useful measurements.

The dashboard prompts keep the workload to direct repository implementation
and one production build per turn: no optional skills, browser automation,
screenshots, or development server. This bounds the search/read/edit/verify
loop consistently across harnesses. The timeout covers the complete two-turn
run. On timeout, the runner terminates the ACP wrapper's whole process group so
a spawned CLI cannot survive and keep the OTLP connection open.

`-agent` accepts:

- `wingman`: Wingman's native coding harness.
- `codex`: the installed Codex CLI through Wingman's ACP adapter and gateway
  backend.
- `claude`: the installed Claude Code CLI through Wingman's ACP adapter and
  gateway backend.

For an apples-to-apples harness comparison, run cases sequentially against the
same gateway and use the same model and effort. Useful pairs are
`wingman/codex` with `gpt-5.6-sol`, and `wingman/claude` with
`claude-sonnet-5` or `claude-opus-5`. Wingman emits the OpenTelemetry GenAI
semantic conventions merged from `feature/telemetry`; Codex and Claude Code use
their native observability settings documented in the
[Codex advanced configuration](https://learn.chatgpt.com/docs/config-file/config-advanced)
and [Claude Code observability](https://code.claude.com/docs/en/agent-sdk/observability)
references. Sequential execution matters because concurrent runs compete for
gateway capacity and distort wall-clock timings.
Use `high` as the normal coding-flow baseline and reserve `max` for a stress
test; larger reasoning budgets can dominate a short harness comparison.

The smaller `invoice` scenario is a cheap smoke test. `invoice_dense` adds
explicit round-trip-minimization guidance and is useful for measuring whether a
harness prompt changes tool batching behavior.
