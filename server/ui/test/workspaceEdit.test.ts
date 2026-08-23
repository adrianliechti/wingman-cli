import assert from "node:assert/strict";
import test from "node:test";
import type { WorkspaceEdit } from "vscode-languageserver-types";
import {
	applyTextEdits,
	combineWorkspaceEditEnvelopes,
	summarizeWorkspaceEdit,
	textEditOperations,
	type WorkspaceEditEnvelope,
} from "../src/workspaceEdit.ts";

test("combines command edits in their server-declared order", () => {
	const uri = "file:///workspace/a.kt";
	const documents = {
		[uri]: { path: "a.kt", revision: "revision", exists: true },
	};
	const combined = combineWorkspaceEditEnvelopes([
		{
			edit: {
				changes: {
					[uri]: [
						{
							range: {
								start: { line: 0, character: 0 },
								end: { line: 0, character: 1 },
							},
							newText: "ab",
						},
					],
				},
			},
			documents,
		},
		{
			edit: {
				changes: {
					[uri]: [
						{
							range: {
								start: { line: 0, character: 2 },
								end: { line: 0, character: 2 },
							},
							newText: "!",
						},
					],
				},
			},
			documents,
		},
	]);

	let content = "a";
	for (const group of textEditOperations(combined).get(uri) ?? []) {
		content = applyTextEdits(content, group.edits);
	}
	assert.equal(content, "ab!");
});

test("applies LSP UTF-16 positions without splitting astral characters", () => {
	assert.equal(
		applyTextEdits("a😀b\r\nsecond", [
			{
				range: {
					start: { line: 0, character: 1 },
					end: { line: 0, character: 3 },
				},
				newText: "x",
			},
			{
				range: {
					start: { line: 1, character: 0 },
					end: { line: 1, character: 6 },
				},
				newText: "next",
			},
		]),
		"axb\r\nnext",
	);
});

test("preserves the declared order of insertions at one position", () => {
	assert.equal(
		applyTextEdits("ab", [
			{
				range: {
					start: { line: 0, character: 1 },
					end: { line: 0, character: 1 },
				},
				newText: "x",
			},
			{
				range: {
					start: { line: 0, character: 1 },
					end: { line: 0, character: 1 },
				},
				newText: "y",
			},
		]),
		"axyb",
	);
});

test("rejects overlapping edits", () => {
	assert.throws(
		() =>
			applyTextEdits("abcdef", [
				{
					range: {
						start: { line: 0, character: 1 },
						end: { line: 0, character: 4 },
					},
					newText: "x",
				},
				{
					range: {
						start: { line: 0, character: 3 },
						end: { line: 0, character: 5 },
					},
					newText: "y",
				},
			]),
		/overlapping/,
	);
});

test("prefers documentChanges and rejects unadvertised file operations", () => {
	const uri = "file:///workspace/a.go";
	const envelope = (edit: WorkspaceEdit): WorkspaceEditEnvelope => ({
		edit,
		documents: {
			[uri]: { path: "a.go", revision: "revision", exists: true },
		},
	});
	const text = textEditOperations(
		envelope({
			changes: {
				[uri]: [
					{
						range: {
							start: { line: 0, character: 0 },
							end: { line: 0, character: 0 },
						},
						newText: "ignored",
					},
				],
			},
			documentChanges: [
				{
					textDocument: { uri, version: null },
					edits: [
						{
							range: {
								start: { line: 0, character: 0 },
								end: { line: 0, character: 0 },
							},
							newText: "used",
						},
					],
				},
			],
		}),
	);
	assert.equal(text.get(uri)?.[0].edits[0].newText, "used");

	assert.throws(
		() =>
			textEditOperations(
				envelope({ documentChanges: [{ kind: "delete", uri }] }),
			),
		/file operation/,
	);
});

test("requires a preview for multi-file or annotated edits", () => {
	const first = "file:///workspace/a.go";
	const second = "file:///workspace/b.go";
	const summary = summarizeWorkspaceEdit({
		edit: {
			changes: {
				[first]: [],
				[second]: [],
			},
		},
		documents: {
			[first]: { path: "a.go", revision: "a", exists: true },
			[second]: { path: "b.go", revision: "b", exists: true },
		},
	});
	assert.deepEqual(summary.files, ["a.go", "b.go"]);
	assert.equal(summary.requiresConfirmation, true);
});
