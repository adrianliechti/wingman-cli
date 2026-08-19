import assert from "node:assert/strict";
import test from "node:test";
import { getInspectAvailability } from "../src/api/capabilities.ts";

test("keeps debugger availability independent from Inspect diagnostics", () => {
	assert.deepEqual(getInspectAvailability(null), {
		inspect: false,
		diagnostics: false,
		debug: false,
	});
	assert.deepEqual(getInspectAvailability({ lsp: true, debug: false }), {
		inspect: true,
		diagnostics: true,
		debug: false,
	});
	assert.deepEqual(getInspectAvailability({ lsp: false, debug: true }), {
		inspect: false,
		diagnostics: false,
		debug: true,
	});
	assert.deepEqual(getInspectAvailability({ lsp: true, debug: true }), {
		inspect: true,
		diagnostics: true,
		debug: true,
	});
});
