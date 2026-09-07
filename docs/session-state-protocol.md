**Session state and web communication — implemented protocol**

The workspace owns a registry of backend runtimes. Each runtime owns its agent, turn manager, and session controllers. Browser navigation selects a backend locally; it never replaces an agent or cancels another browser's work.

The original analysis is in [session-workspace-state.md](session-workspace-state.md). The follow-up correctness review and user-visible improvements are in [session-architecture-review.md](session-architecture-review.md). This document describes the implementation and its deliberate limits.

**Ownership**

| Owner | Responsibilities |
| --- | --- |
| `Server` | Open workspace, immutable workspace/instance identity, filesystem and language services, backend registry, connection lifetime |
| `backendRuntime` | One `code.Agent` and `TurnManager`, session registry, creation receipts, task pumps |
| `sessionController` | Complete visible transcript and metadata, version, subscribers, pending prompts, command receipts |
| `TurnManager` | Execution, steering, FIFO queue, cancellation and queue persistence |
| `WorkspaceClient` | One WebSocket, subscription lifecycle, HTTP commands, uncertain input delivery, draft creation deduplication |
| `SessionStore` | Immutable authoritative views; revision validation and explicit change application |
| React Query | Workspace resources, backend catalogs, session summaries, tasks and schedules |
| Browser navigation | Tabs, pane selection, selected backend, local drafts and draft settings |

The server implementation lives in [runtime.go](../server/runtime.go), [session.go](../server/session.go), and [events.go](../server/events.go). The browser implementation lives in [state/](../server/ui/src/state/). [useWebSocket.ts](../server/ui/src/hooks/useWebSocket.ts) contains React bindings and action feedback, without history reconstruction.

**Identity and lifetime**

`GET /api/v2/bootstrap` returns protocol version `2`, the backend catalog, and:

```ts
type WorkspaceScope = { workspaceId: string; instanceId: string };
type SessionRef = { workspaceId: string; backendId: string; sessionId: string };
```

The workspace ID hashes the canonical opened root. The instance ID is a fresh UUID for each server lifetime. These connection identities are independent of the existing project storage layout. Session IDs remain opaque provider IDs. Browser map/query keys serialize the backend and native ID as a tuple, avoiding delimiter collisions.

API requests carry `X-Wingman-Instance`. WebSockets and browser download/preview URLs carry `instance` in the query string. Missing or stale instances receive HTTP 409 before the operation runs. Bootstrap is the only unscoped workspace API. Native workspace replacement also checks the current instance.

Backend startup is shared per backend ID. A slow external process starts outside the registry mutex, so it does not block an already-running backend. Startup that finishes after cancellation is joined and its returned agent is closed without publication.

Closing the server stops operation admission, cancels execution and closes event sockets. It joins turns, admitted backend operations, and startup before releasing their resources. Backend HTTP reads observe both request and workspace cancellation. Session deletion joins the cancelled turn's final callbacks and queue writes before deleting backend history.

On disconnect, the browser checks bootstrap. A changed instance stops reconnection and presents a reload action while retaining editor buffers and unsent drafts. The initiating native window retains its explicit folder-opening flow and existing unsaved-edit confirmation. A connection generation prevents an old bootstrap response from changing a restarted connection. Temporary bootstrap failures retry without reporting workspace replacement.

Each `WorkspaceClient` owns an HTTP transport bound to its immutable instance ID. Session commands do not consult a mutable page-global client for their identity. Page resource helpers use the transport installed at bootstrap. The layout reducer alone creates a fallback draft when the last chat closes, keeping that chat's backend; draft IDs are generated at dispatch so reducer replay is deterministic.

Each session controller has an immutable epoch UUID and increasing revision. Deletion retains a tombstone for the instance lifetime. An old epoch cannot load/recreate a session through a command. Following an existing `/:backend/:session` URL selects that backend locally and subscribes to that session.

**HTTP commands**

All paths below have the `/api/v2` prefix.

| Request | Purpose |
| --- | --- |
| `GET /backends/:backend/settings` | Backend defaults/catalog for local drafts |
| `GET /backends/:backend/sessions` | Session summaries for that backend |
| `POST /backends/:backend/sessions` | `{type: "create", id}`; create once per draft request ID |
| `POST /backends/:backend/sessions/:session/commands` | Submit a typed session command with `id` and `epoch` |
| Existing task/schedule operations below `/backends/:backend/sessions/:session` | Explicitly owned task resources |

A single command endpoint keeps validation and receipt handling together. Its `type` is one of:

- `send`: visible text/files/images and `follow_up` or `steer` intent. The request ID is the input ID.
- `queue_update`, `queue_remove`: address `inputId`.
- `queue_resume`, `queue_clear`.
- `cancel`: `clearQueue` explicitly chooses whether queued follow-ups are discarded.
- `settings`: optional `model`, `effort`, and `mode` fields.
- `prompt_response`: `promptId`, action, optional structured content and confirmation scope.
- `delete`.

A successful operation returns `{id, ref, epoch, outcome}`. It does not return a competing transcript snapshot. HTTP acceptance and execution completion are distinct; only the subscription changes the authoritative browser view.

Creation and command receipts remain available for the server/controller lifetime, including after a turn completes. Repeating the same ID and body returns the recorded outcome; reusing an ID with different content conflicts. Queue persistence failures reject the mutation and preserve the queue. An unreadable queue cannot be overwritten by a mutation; later access retries the read. Loading queue storage holds only that session's lock. Restored queues stay paused until explicitly resumed, and successful persistence clears a previous queue write error.

The browser shares an in-flight creation per backend/draft pair and preserves the tab's React key when adopting the returned session. Draft settings are local, then applied before its first input. Concurrent sends of the same envelope share one HTTP attempt and result. The client copies attachment arrays before awaiting synchronization. On an uncertain send response, the composer retains the input and a retry reuses the same request ID and epoch. Commands waiting for synchronization reject promptly when the client stops. There is no automatic replay of uncertain mutations against a new instance or epoch. Receipts are not a durable exactly-once guarantee across process restarts.

Normal commands and loads serialize on the session operation mutex. Every command, including a prompt response, reserves its ID and fingerprint in one receipt ledger before waiting for that mutex. Identical duplicates wait for the same result; a different body conflicts even while the first request is running. Prompt responses resolve without the operation mutex so they can answer a prompt produced by a load or settings operation. Removing a pending prompt under the projection mutex selects the single winning response. Command responses are written after releasing receipt and projection locks. All pending prompts are represented in snapshots, including an explicit empty array after resolution. Internal task delivery has a separate deduplication map and cannot collide with client request IDs.

**Subscription protocol**

The event socket is `/api/v2/events?instance=...`. It accepts `subscribe`, `unsubscribe`, and workspace `focus` controls. It does not accept application commands. Terminal byte streams keep their dedicated sockets.

```ts
{ type: "subscribe", subscriptionId, ref }
{ type: "unsubscribe", subscriptionId }
```

The first response to a subscription is a snapshot containing `ref`, `subscriptionId`, `epoch`, `revision`, `entries`, and the complete metadata `state`. An unloaded view explicitly starts in `loading`; loading saved history produces ordered updates followed by `ready`, or an explicit `error`. Observing an unknown ID never produces an empty ready session. Multiple observers share one backend load.

Registration and snapshot enqueue happen under the same mutex as projection updates. A change cannot overtake the initial snapshot or disappear between snapshot capture and registration. Subscriptions are identified by socket and subscription ID; two subscriptions on the same socket remain independent. The socket's subscription limit returns an explicit error. Session updates go only to subscribers of that view. Workspace resource invalidations remain small broadcast notifications.

```ts
{
  type: "session.update",
  subscriptionId, ref, epoch,
  previousRevision, revision,
  changes: [/* applied atomically */]
}
```

The change types are `entry.upsert`, `entry.append`, `entries.remove`, `entries.replace`, and `state.replace`. Metadata includes readiness/deletion status, phase, nullable error, usage, all pending prompts, the visible input queue, queue pause/steering capability, settings, and tool progress. Empty collections and cleared fields replace old state.

The visible queue contains waiting user inputs. Active and accepted steering inputs appear in the transcript, and background task inputs are omitted from the queue panel. This prevents a single input appearing twice in the UI.

The store ignores duplicate revisions, older snapshots on an already-synchronized subscription, and obsolete subscriptions. A gap, epoch mismatch, or append to an unknown entry requests a fresh subscription/snapshot. A failed batch commits none of its earlier changes. Metadata replacement accepts only metadata fields; it cannot overwrite the event's identity, revision, or transcript. Starting a new subscription clears an old load failure so recovery can proceed. Reconnection always starts with a snapshot; there is no replay log or browser history merge.

**Backend normalization and persistence**

Live transcript entries remain in the controller while a response is unfinished, so another browser or a reconnect receives the complete prefix. Provider retry/reset cleanup happens in Go. Hidden instructions and background input content stay outside the visible transcript.

Input IDs propagate through the native harness and ACP adapter into retained user messages, including steering. Text/tool identifiers survive commit; ACP text and reasoning without IDs receive identities at stream boundaries. Old history without these IDs gets deterministic replay identities within its new view epoch.

The implementation uses full `entries.replace` changes at history-load and commit boundaries. Incremental text uses append/upsert changes. This makes reconciliation explicit and keeps the browser simple, at the cost of transmitting retained history at each commit. A future optimization can diff canonical entries by identity without changing the ownership contract.

Queued inputs persist their execution content, original visible envelope, and origin together. Older queue records are normalized at the Go boundary. Web projection no longer depends on a memory-only `turnMeta` map. Background delivery remains attached to its runtime and cannot recreate deleted work.

The backend journal owns conversation persistence. Web turn finalization no longer calls the built-in compatibility `Save` method. Existing journal and disk-format readers remain supported. Web model/effort changes affect the addressed session; TUI default-setting behavior is preserved through the existing agent options.

ACP configuration, usage, commands and mode notifications can update the projection outside a send stream. Prompts without session correlation are not routed to whichever session happened to be active last. The web built-in runtime requires explicit correlation; the single-client TUI retains its existing fallback behavior.

**Validation and limits**

Regression coverage includes live reconnect snapshots, queue restoration/editing, missing history, atomic subscription ordering, backend identity collisions, deletion tombstones, prompt resolution during loads/settings, receipt deduplication during and after execution, persistence failures, stable committed entry identity, and socket shutdown. Adversarial tests block queue reads, startup, admission, and terminal finalization to control race ordering. A deterministic 2,000-step browser-store test checks convergence after dropped updates, duplicates, and resubscriptions. Browser tests exercise a second observer and page reload during a live response, lost-receipt retry without another model invocation, preservation of a draft after workspace replacement, and preservation of its backend after closing the last session.

Verification on 7 September 2026:

- `go test -p 1 ./...`: passed (61 packages with tests).
- Race detector: passed for `pkg/agent`, `pkg/code`, `pkg/code/agent`, `pkg/code/acp`, `server`, and `app`. Session, backend startup, shutdown, and turn-manager tests also passed ten consecutive runs under the race detector.
- `npm run test:unit`: 73 passed.
- Full Playwright suite through `TestWebUIE2ECodingAgentWorkflows`: 60 passed.
- `npm run build` and `git diff --check`: passed. Vite still reports its large-bundle warning.
- Repository-wide UI lint remains failing on React ref/effect/compiler rules in existing UI code. The new state modules pass lint; remaining diagnostics point to unchanged source lines. A clean `HEAD` also fails lint.

The new protocol retires `/api/agent`, unscoped chat/session APIs, WebSocket send/sync commands, browser history matching, and HTTP-load promise maps. Resource APIs and terminal sockets remain, with the instance check added.

The legacy project-directory key collision identified in the review remains a **separate storage migration**. This refactor does not relocate or silently claim ambiguous existing session/memory directories. A migration needs an ownership record and an explicit way to resolve legacy directories whose original root cannot be established. Connection identity hashing fixes protocol/cache isolation, not that pre-existing disk-path collision.

Backend runtimes and session views currently stay resident until workspace shutdown. Transcript pagination, controller eviction, durable receipts across restarts, and incremental canonical commit diffs are separate optimizations rather than another synchronization mechanism in the browser.
