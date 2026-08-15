import assert from "node:assert/strict";
import test from "node:test";
import { discardUncommittedStreamEntries } from "../src/streamEntries.ts";

test("discards failed deltas and unexecuted partial tools", () => {
	const entries = [
		{ id: "user" },
		{ id: "committed-tool", toolPartial: false },
		{ id: "partial-tool", toolPartial: true },
		{ id: "failed-text" },
	];

	assert.deepEqual(
		discardUncommittedStreamEntries(entries, new Set(["failed-text"])),
		entries.slice(0, 2),
	);
	assert.deepEqual(discardUncommittedStreamEntries(entries), [
		entries[0],
		entries[1],
		entries[3],
	]);
});
