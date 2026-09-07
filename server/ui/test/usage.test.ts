import assert from "node:assert/strict";
import test from "node:test";
import {
	contextRemainingPercent,
	shouldShowContextIndicator,
} from "../src/utils/usage.ts";

test("shows context usage only when less than a quarter remains", () => {
	assert.equal(shouldShowContextIndicator(74, 100), false);
	assert.equal(shouldShowContextIndicator(75, 100), false);
	assert.equal(shouldShowContextIndicator(76, 100), true);
	assert.equal(shouldShowContextIndicator(100, 100), true);
	assert.equal(shouldShowContextIndicator(0, 100), false);
	assert.equal(shouldShowContextIndicator(10, 0), false);
	assert.equal(contextRemainingPercent(82, 100), 18);
});
