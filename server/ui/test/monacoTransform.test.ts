import assert from "node:assert/strict";
import test from "node:test";
import { isEditorTransformResponse } from "../src/monacoTransform.ts";

test("validates inline transformation responses", () => {
	assert.equal(
		isEditorTransformResponse({
			version: 4,
			edit: {
				expected_text: "old",
				replacement: "new",
				range: {
					start_line: 2,
					start_column: 3,
					end_line: 2,
					end_column: 6,
				},
			},
		}),
		true,
	);
	assert.equal(isEditorTransformResponse({ version: 4, edit: null }), true);
});

test("rejects malformed or reversed transformation responses", () => {
	assert.equal(
		isEditorTransformResponse({
			version: 4,
			edit: {
				expected_text: "old",
				replacement: "new",
				range: {
					start_line: 2,
					start_column: 6,
					end_line: 2,
					end_column: 3,
				},
			},
		}),
		false,
	);
	assert.equal(
		isEditorTransformResponse({
			version: 4,
			edit: {
				expected_text: "",
				replacement: "insertion",
				range: {
					start_line: 2,
					start_column: 3,
					end_line: 2,
					end_column: 3,
				},
			},
		}),
		false,
	);
	assert.equal(
		isEditorTransformResponse({
			version: "4",
			edit: null,
		}),
		false,
	);
	assert.equal(
		isEditorTransformResponse({
			version: 4,
			edit: { expected_text: "old", replacement: 3, range: {} },
		}),
		false,
	);
});
