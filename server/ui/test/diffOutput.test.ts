import assert from "node:assert/strict";
import test from "node:test";
import {
	looksLikeDiffOutput,
	shouldRenderDiff,
} from "../src/components/chat/diffOutput.ts";

test("detects ACP flattened diffs without relying on a tool title", () => {
	const output = [
		"/workspace/server/ui/src/api/editor.ts",
		" export interface Request {",
		"-  content: string;",
		"+  replacement: string;",
		" }",
	].join("\n");

	assert.equal(looksLikeDiffOutput(output), true);
	assert.equal(shouldRenderDiff(undefined, undefined, output), true);
});

test("detects unified and apply-patch output", () => {
	assert.equal(
		looksLikeDiffOutput(
			"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new",
		),
		true,
	);
	assert.equal(
		looksLikeDiffOutput(
			"*** Begin Patch\n*** Update File: a.go\n@@\n-old\n+new\n*** End Patch",
		),
		true,
	);
});

test("does not classify ordinary prefixed output as a diff", () => {
	assert.equal(
		looksLikeDiffOutput("Build summary\n+ passed\n- skipped"),
		false,
	);
	assert.equal(looksLikeDiffOutput("GET /health\n+ passed\n- skipped"), false);
	assert.equal(looksLikeDiffOutput("+ just one log line"), false);
	assert.equal(looksLikeDiffOutput("/workspace/file.go\nplain output"), false);
});

test("prefers semantic tool kind even before result content arrives", () => {
	assert.equal(shouldRenderDiff("edit", "Anything", ""), true);
	assert.equal(shouldRenderDiff("delete", "Anything", ""), true);
	assert.equal(shouldRenderDiff("execute", "Anything", "hello"), false);
});
