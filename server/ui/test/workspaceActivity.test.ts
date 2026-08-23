import assert from "node:assert/strict";
import test from "node:test";
import {
	languageServerActivity,
	managedToolActivities,
} from "../src/components/workspaceActivitySources.ts";

test("managed tool activities distinguish checks, installs, and updates", () => {
	for (const [phase, action] of [
		["checking", "Checking"],
		["installing", "Installing"],
		["updating", "Updating"],
	] as const) {
		const [activity] = managedToolActivities(
			{
				state: "installing",
				phase,
				tool: "debugpy",
				label: "Python debugger",
				current: 2,
				total: 4,
			},
			true,
		);
		assert.equal(activity.label, `${action} Python debugger`);
		assert.equal(activity.scope, "2 / 4");
		assert.equal(activity.percentage, 50);
	}
});

test("language activity renders its label and a neutral analysis hint", () => {
	const activity = languageServerActivity({
		server: "custom-lsp",
		label: "A bespoke language",
		project: "packages/example",
		analyzing: true,
		operations: [
			{ title: "Reading symbols", message: "library.kt", percentage: 40 },
		],
	});

	assert.equal(activity.label, "Indexing A bespoke language");
	assert.equal(
		activity.hint,
		"Language features may be incomplete while background analysis finishes.",
	);
	assert.equal(activity.scope, "packages/example");
	assert.deepEqual(activity.operations, [
		{ label: "Reading symbols", detail: "library.kt", percentage: 40 },
	]);
});
