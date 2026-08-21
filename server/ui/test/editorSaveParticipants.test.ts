import assert from "node:assert/strict";
import test from "node:test";
import {
	registerEditorSaveParticipant,
	runEditorSaveParticipants,
} from "../src/editorSaveParticipants.ts";

test("save participants are ordered and disposable", async () => {
	const calls: string[] = [];
	const disposeFirst = registerEditorSaveParticipant(
		"src/main.ts",
		async () => {
			calls.push("first");
			return true;
		},
	);
	const disposeSecond = registerEditorSaveParticipant(
		"src/main.ts",
		async () => {
			calls.push("second");
		},
	);

	assert.equal(await runEditorSaveParticipants("src/main.ts"), true);
	assert.deepEqual(calls, ["first", "second"]);
	disposeFirst();
	assert.equal(await runEditorSaveParticipants("src/main.ts"), false);
	assert.deepEqual(calls, ["first", "second", "second"]);
	disposeSecond();
});

test("save participant failures do not block later participants", async () => {
	let completed = false;
	const disposeFailure = registerEditorSaveParticipant("main.py", async () => {
		throw new Error("language service unavailable");
	});
	const disposeSuccess = registerEditorSaveParticipant("main.py", async () => {
		completed = true;
	});

	await assert.doesNotReject(runEditorSaveParticipants("main.py"));
	assert.equal(completed, true);
	disposeFailure();
	disposeSuccess();
});

test("concurrent saves share one participant run", async () => {
	let calls = 0;
	let release!: () => void;
	const blocked = new Promise<void>((resolve) => {
		release = resolve;
	});
	const dispose = registerEditorSaveParticipant("main.go", async () => {
		calls++;
		await blocked;
		return true;
	});

	const first = runEditorSaveParticipants("main.go");
	const second = runEditorSaveParticipants("main.go");
	assert.equal(first, second);
	release();
	await Promise.all([first, second]);
	assert.equal(calls, 1);
	dispose();
});
