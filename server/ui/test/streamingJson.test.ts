import assert from "node:assert/strict";
import test from "node:test";
import { parseTodoItems } from "../src/streamingJson.ts";

test("parses complete and truncated todo arguments", () => {
	assert.deepEqual(
		parseTodoItems(
			`{"items":[{"content":"Fix parser","status":"completed"},{"content":"Add tests","status":"in_progress"},{"content":"Upd`,
		),
		[
			{ content: "Fix parser", status: "completed" },
			{ content: "Add tests", status: "in_progress" },
			{ content: "Upd" },
		],
	);
	assert.deepEqual(
		parseTodoItems(
			`{"items":[{"content":"Fix parser","status":"pending"},{"content":"`,
		),
		[{ content: "Fix parser", status: "pending" }],
	);
});

test("rejects malformed todo arguments", () => {
	assert.deepEqual(parseTodoItems(`{"items":true}`), []);
	assert.deepEqual(parseTodoItems(`]`), []);
	assert.deepEqual(parseTodoItems(`{"items":[}`), []);
	assert.deepEqual(
		parseTodoItems(`{"items":[{"content":"bad","status":1}]}`),
		[],
	);
});
