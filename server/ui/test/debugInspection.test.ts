import assert from "node:assert/strict";
import test from "node:test";
import type { DebugInspection, DebugSession } from "../src/api/debug.ts";
import {
	debugInspectionPollInterval,
	preserveDebugInspection,
	preserveDebugOutput,
} from "../src/debugInspection.ts";

function inspection(
	version: number,
	options: {
		state?: DebugSession["state"];
		frames?: DebugInspection["frames"];
		threads?: DebugInspection["threads"];
		output?: string;
	} = {},
): DebugInspection {
	return {
		session: {
			session_id: "debug-1",
			adapter: "go",
			language: "go",
			request: "launch",
			io: "output",
			capabilities: { supports_step_back: false },
			state_version: version,
			state: options.state ?? "stopped",
			started_at: "2026-08-23T00:00:00Z",
		},
		output: options.output ?? "",
		threads: options.threads ?? [],
		frames: options.frames ?? [],
	};
}

test("debug inspection polling stops without a live session", () => {
	assert.equal(debugInspectionPollInterval(undefined, 500, 1_500), false);
	assert.equal(
		debugInspectionPollInterval(
			inspection(1, { state: "terminated" }),
			500,
			1_500,
		),
		false,
	);
	assert.equal(
		debugInspectionPollInterval(
			inspection(1, { state: "running" }),
			500,
			1_500,
		),
		500,
	);
	assert.equal(debugInspectionPollInterval(inspection(1), 500, 1_500), 1_500);
});

test("inspection accepts frames that arrive later in the same stop", () => {
	const previous = inspection(4);
	const next = inspection(4, {
		frames: [{ id: 9, name: "main", line: 12, column: 1 }],
	});

	assert.equal(preserveDebugInspection(previous, next), next);
});

test("inspection rejects stale epochs and stabilizes a populated tree", () => {
	const frame = { id: 9, name: "main", line: 12, column: 1 };
	const previous = inspection(4, { frames: [frame] });

	assert.equal(preserveDebugInspection(previous, inspection(3)), previous);
	assert.equal(preserveDebugInspection(previous, inspection(4)), previous);
	assert.notEqual(preserveDebugInspection(previous, inspection(5)), previous);
});

test("debug output rejects stale epochs and retains final output", () => {
	const previous = inspection(4, { output: "program output" });

	assert.equal(preserveDebugOutput(previous, inspection(3)), previous);
	assert.deepEqual(preserveDebugOutput(previous, inspection(4)), {
		...inspection(4),
		output: "program output",
	});
});
