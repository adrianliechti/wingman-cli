import assert from "node:assert/strict";
import test from "node:test";
import type { WorkspaceSearchFile } from "../src/api/search.ts";
import { workspaceSearchEdit } from "../src/utils/workspaceSearch.ts";

test("builds revision-checked workspace edits from search matches", () => {
	const files: WorkspaceSearchFile[] = [
		{
			path: "src/example.ts",
			revision: "revision-1",
			matches: [
				{
					line: 3,
					column: 5,
					end_column: 8,
					before: "let ",
					text: "cat",
					after: " = true",
					replacement: "dog",
				},
				{
					line: 8,
					column: 2,
					end_column: 5,
					before: " ",
					text: "cat",
					after: "",
					replacement: "dog",
				},
			],
		},
	];
	const envelope = workspaceSearchEdit([
		{ ...files[0], matches: [files[0].matches[1]] },
	]);
	const uri = `wingman-search:${encodeURIComponent(files[0].path)}`;
	assert.deepEqual(envelope.documents[uri], {
		path: files[0].path,
		revision: "revision-1",
		exists: true,
	});
	assert.deepEqual(envelope.edit?.changes?.[uri], [
		{
			range: {
				start: { line: 7, character: 1 },
				end: { line: 7, character: 4 },
			},
			newText: "dog",
		},
	]);
});
