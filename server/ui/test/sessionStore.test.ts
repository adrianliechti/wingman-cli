import assert from "node:assert/strict";
import test from "node:test";
import {
	SessionStore,
	emptySession,
	sessionKey,
	type SessionEvent,
	type SessionChange,
} from "../src/state/sessionStore.ts";
import type { ChatEntry } from "../src/types/session.ts";

const ref = { workspaceId: "workspace", backendId: "one", sessionId: "same" };
const key = sessionKey(ref.backendId, ref.sessionId);
function snapshot(overrides: Partial<SessionEvent> = {}): SessionEvent {
	return {
		type: "session.snapshot",
		subscriptionId: "sub",
		ref,
		epoch: "epoch",
		revision: 2,
		previousRevision: 0,
		state: { ...emptySession(key), status: "ready" },
		entries: [{ id: "text", type: "assistant", content: "live prefix" }],
		...overrides,
	};
}
function update(overrides: Partial<SessionEvent> = {}): SessionEvent {
	return {
		type: "session.update",
		subscriptionId: "sub",
		ref,
		epoch: "epoch",
		previousRevision: 2,
		revision: 3,
		changes: [{ type: "entry.append", id: "text", text: " suffix" }],
		...overrides,
	};
}
function initialized() {
	const store = new SessionStore();
	store.expect(key, "sub");
	store.apply(snapshot());
	return store;
}

test("unrequested events do not make a session loaded", () => {
	const store = new SessionStore();
	assert.equal(store.apply(snapshot()), "ignored");
	assert.deepEqual(store.getSnapshot(), {});
	store.expect(key, "sub");
	assert.equal(store.apply(update()), "ignored");
	assert.equal(store.getSnapshot()[key].synchronized, false);
});
test("snapshots recover live text and explicitly clear prompts and errors", () => {
	const store = initialized();
	store.apply(
		update({
			changes: [
				{
					type: "state.replace",
					state: {
						...emptySession(key),
						status: "ready",
						prompts: [{ id: "prompt", kind: "confirm", message: "proceed" }],
						error: "old error",
					},
				},
			],
		}),
	);
	store.disconnect();
	store.expect(key, "reconnected");
	assert.equal(
		store.apply(
			snapshot({ subscriptionId: "reconnected", epoch: "new", revision: 0 }),
		),
		"applied",
	);
	assert.equal(store.getSnapshot()[key].entries[0].content, "live prefix");
	assert.deepEqual(store.getSnapshot()[key].prompts, []);
	assert.equal(store.getSnapshot()[key].error, null);
});
test("duplicates are ignored and gaps or wrong epochs require a fresh snapshot", () => {
	const store = initialized();
	assert.equal(store.apply(update()), "applied");
	assert.equal(store.apply(update()), "ignored");
	assert.equal(
		store.getSnapshot()[key].entries[0].content,
		"live prefix suffix",
	);
	assert.equal(
		store.apply(update({ previousRevision: 4, revision: 5 })),
		"resubscribe",
	);
	assert.equal(store.apply(update({ epoch: "other" })), "resubscribe");
	assert.equal(store.getSnapshot()[key].revision, 3);
});
test("late events from a replaced subscription cannot overwrite a new snapshot", () => {
	const store = initialized();
	store.expect(key, "new-sub");
	store.apply(
		snapshot({
			subscriptionId: "new-sub",
			epoch: "new-epoch",
			revision: 0,
			entries: [],
		}),
	);
	assert.equal(store.apply(snapshot()), "ignored");
	assert.equal(store.apply(update()), "ignored");
	assert.deepEqual(store.getSnapshot()[key].entries, []);
});
test("backend identity separates identical native session ids", () => {
	const store = initialized();
	const other = sessionKey("two", "same");
	store.expect(other, "other-sub");
	store.apply(
		snapshot({
			ref: { ...ref, backendId: "two" },
			subscriptionId: "other-sub",
			entries: [],
		}),
	);
	store.apply(update());
	assert.equal(
		store.getSnapshot()[key].entries[0].content,
		"live prefix suffix",
	);
	assert.deepEqual(store.getSnapshot()[other].entries, []);
});
test("entry batches are atomic and unknown append targets trigger recovery", () => {
	const store = initialized();
	let notifications = 0;
	store.subscribe(() => notifications++);
	store.apply(
		update({
			changes: [
				{
					type: "entry.upsert",
					entry: { id: "tool", type: "tool", content: "", toolResult: "done" },
				},
				{ type: "entries.remove", ids: ["text"] },
			],
		}),
	);
	assert.equal(notifications, 1);
	assert.deepEqual(
		store.getSnapshot()[key].entries.map((entry) => entry.id),
		["tool"],
	);
	assert.equal(
		store.apply(update({ previousRevision: 3, revision: 4 })),
		"resubscribe",
	);
	assert.equal(notifications, 1);
});
test("deleted session state replaces active work and all pending prompts", () => {
	const store = initialized();
	store.apply(
		update({
			changes: [
				{
					type: "state.replace",
					state: { ...emptySession(key), status: "deleted" },
				},
			],
		}),
	);
	assert.equal(store.getSnapshot()[key].status, "deleted");
	assert.deepEqual(store.getSnapshot()[key].pendingInputs, []);
});

test("duplicate snapshots cannot roll back newer deltas on the same subscription", () => {
	const store = initialized();
	store.apply(update());
	assert.equal(store.apply(snapshot()), "ignored");
	assert.equal(store.getSnapshot()[key].revision, 3);
	assert.equal(
		store.getSnapshot()[key].entries[0].content,
		"live prefix suffix",
	);
});

test("a new subscription clears an expired load failure before retrying", () => {
	const store = initialized();
	store.apply({
		...snapshot(),
		type: "session.error",
		message: "temporary load error",
	});
	store.expect(key, "retry");
	assert.equal(store.getSnapshot()[key].status, "loading");
	assert.equal(store.getSnapshot()[key].error, null);
});

test("a failed batch does not commit its earlier valid changes", () => {
	const store = initialized();
	const before = store.getSnapshot();
	const result = store.apply(
		update({
			changes: [
				{ type: "entry.append", id: "text", text: " must roll back" },
				{ type: "entry.append", id: "missing", text: "invalid" },
			],
		}),
	);
	assert.equal(result, "resubscribe");
	assert.equal(store.getSnapshot(), before);
	assert.equal(before[key].entries[0].content, "live prefix");
});

test("metadata replacement cannot overwrite protocol identity or transcript", () => {
	const store = initialized();
	assert.equal(
		store.apply(
			update({
				changes: [
					{ type: "entry.append", id: "text", text: " suffix" },
					{
						type: "state.replace",
						state: { ...emptySession("unrelated"), status: "ready" },
					},
				],
			}),
		),
		"applied",
	);
	const view = store.getSnapshot()[key];
	assert.equal(view.id, key);
	assert.equal(view.epoch, "epoch");
	assert.equal(view.revision, 3);
	assert.equal(view.synchronized, true);
	assert.equal(view.entries[0].content, "live prefix suffix");
	assert.equal(
		store.apply(update({ previousRevision: 3, revision: 4 })),
		"applied",
	);
});

test("seeded streams converge after dropped events, duplicates, and resubscriptions", () => {
	const store = initialized();
	let entries: ChatEntry[] = [...store.getSnapshot()[key].entries];
	let revision = 2,
		subscriptionId = "sub",
		seed = 0x51f15e;
	const random = () => {
		seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
		return seed;
	};
	let recovered = 0;
	for (let step = 0; step < 2000; step++) {
		const oldEntries = entries;
		const id = `entry-${random() % 17}`;
		let change: SessionChange;
		switch (random() % 4) {
			case 0: {
				const entry: ChatEntry = {
					id,
					type: "assistant",
					content: `value-${step}`,
				};
				change = { type: "entry.upsert", entry };
				entries = entries.some((item) => item.id === id)
					? entries.map((item) => (item.id === id ? entry : item))
					: [...entries, entry];
				break;
			}
			case 1: {
				const target = entries[0];
				if (target) {
					change = { type: "entry.append", id: target.id, text: "x" };
					entries = entries.map((item) =>
						item.id === target.id
							? { ...item, content: item.content + "x" }
							: item,
					);
				} else {
					change = { type: "entries.replace", entries: [] };
					entries = [];
				}
				break;
			}
			case 2:
				change = { type: "entries.remove", ids: [id] };
				entries = entries.filter((item) => item.id !== id);
				break;
			default:
				entries = entries.slice(-5);
				change = { type: "entries.replace", entries };
		}
		const event = update({
			subscriptionId,
			previousRevision: revision,
			revision: ++revision,
			changes: [change],
		});
		if (random() % 7 === 0) continue; // The server advances while this connection misses an event.
		const previous = store.getSnapshot()[key];
		Object.freeze(previous.entries);
		previous.entries.forEach(Object.freeze);
		const result = store.apply(event);
		if (result === "resubscribe") {
			subscriptionId = `recovery-${++recovered}`;
			store.expect(key, subscriptionId);
			store.apply(snapshot({ subscriptionId, revision, entries }));
		} else {
			assert.equal(result, "applied");
			assert.equal(store.apply(event), "ignored");
			assert.equal(
				store.apply(
					snapshot({
						subscriptionId,
						revision: revision - 1,
						entries: oldEntries,
					}),
				),
				"ignored",
			);
		}
		assert.deepEqual(store.getSnapshot()[key].entries, entries, `step ${step}`);
		assert.equal(store.getSnapshot()[key].revision, revision);
	}
	assert.ok(recovered > 100, "exercise many independent recovery boundaries");
});
