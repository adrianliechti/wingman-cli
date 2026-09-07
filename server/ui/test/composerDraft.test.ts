import assert from "node:assert/strict";
import test from "node:test";
import { ComposerDraft } from "../src/state/composerDraft.ts";

const content = {
	text: "Review this file",
	files: ["main.go"],
	images: [
		{
			id: "image-1",
			name: "diagram.png",
			dataUrl: "data:image/png;base64,AA==",
		},
	],
	editingQueueId: "queued-1",
};

test("accepted delivery clears the submitted content and queue edit", async () => {
	const draft = new ComposerDraft();
	draft.update(content);
	assert.equal(await draft.submit(() => true), true);
	assert.deepEqual(draft.getSnapshot(), {
		text: "",
		files: [],
		images: [],
		editingQueueId: null,
		submitting: false,
		error: null,
	});
});

test("a late receipt preserves edits and attachments added during delivery", async () => {
	const draft = new ComposerDraft();
	draft.update(content);
	const receipt = Promise.withResolvers<boolean>();
	const sending = draft.submit((submitted) => {
		assert.deepEqual(submitted.files, content.files);
		assert.deepEqual(submitted.images, content.images);
		assert.equal(submitted.text, content.text);
		assert.equal(submitted.editingQueueId, content.editingQueueId);
		return receipt.promise;
	});
	draft.update({
		text: "Next message",
		files: ["next.go"],
		images: [],
		editingQueueId: null,
	});
	receipt.resolve(true);
	await sending;
	assert.deepEqual(draft.getSnapshot(), {
		text: "Next message",
		files: ["next.go"],
		images: [],
		editingQueueId: null,
		submitting: false,
		error: null,
	});
});

test("delivery belongs to the draft after its observer unmounts", async () => {
	const draft = new ComposerDraft();
	draft.update(content);
	let previousNotifications = 0;
	const unmount = draft.subscribe(() => previousNotifications++);
	const receipt = Promise.withResolvers<boolean>();
	const sending = draft.submit(() => receipt.promise);
	assert.equal(previousNotifications, 1);
	unmount();
	assert.equal(draft.getSnapshot().submitting, true);
	assert.equal(await draft.submit(() => assert.fail("duplicate send")), false);
	let notifications = 0;
	const unsubscribe = draft.subscribe(() => notifications++);
	receipt.resolve(true);
	await sending;
	unsubscribe();
	assert.equal(previousNotifications, 1);
	assert.equal(notifications, 1);
	assert.equal(draft.getSnapshot().text, "");
	assert.equal(draft.getSnapshot().submitting, false);
});

for (const failure of ["rejection", "exception"] as const) {
	test(`a delivery ${failure} preserves the complete draft for retry`, async () => {
		const draft = new ComposerDraft();
		draft.update(content);
		assert.equal(
			await draft.submit(() => {
				if (failure === "exception") throw new Error("disconnected");
				return false;
			}),
			false,
		);
		assert.deepEqual(draft.getSnapshot(), {
			...content,
			submitting: false,
			error: "Message was not accepted. Your draft has been kept.",
		});
		assert.equal(await draft.submit(() => true), true);
		assert.equal(draft.getSnapshot().error, null);
		assert.equal(draft.getSnapshot().text, "");
	});
}

test("delivery in one tab does not change another tab's draft", async () => {
	const first = new ComposerDraft();
	const second = new ComposerDraft();
	first.update(content);
	second.update({ text: "Keep the other draft" });
	await first.submit(() => true);
	assert.equal(second.getSnapshot().text, "Keep the other draft");
	assert.equal(second.getSnapshot().submitting, false);
});
