import assert from "node:assert/strict";
import test from "node:test";
import {
	isTabPredictionResponse,
	replacesEntireTabDocument,
	tabCacheKey,
} from "../src/monacoTab.ts";

test("validates the Tab wire response", () => {
	assert.equal(
		isTabPredictionResponse({
			version: 4,
			edit: {
				insert_text: "replacement",
				expected_text: "old\ntext",
				range: {
					start_line: 2,
					start_column: 3,
					end_line: 3,
					end_column: 5,
				},
			},
		}),
		true,
	);
	assert.equal(isTabPredictionResponse({ version: 4, edit: null }), true);
	assert.equal(
		isTabPredictionResponse({
			version: 4,
			edit: {
				insert_text: "replacement",
				expected_text: "old",
				range: { start_line: 2, end_line: 2 },
			},
		}),
		false,
	);
	assert.equal(isTabPredictionResponse({ version: "4", edit: null }), false);
	assert.equal(
		isTabPredictionResponse({
			version: 4,
			edit: {
				insert_text: "replacement",
				expected_text: "old",
				range: {
					start_line: 2,
					start_column: 5,
					end_line: 2,
					end_column: 4,
				},
			},
		}),
		false,
	);
	assert.equal(
		isTabPredictionResponse({
			version: 4,
			edit: {
				insert_text: "replacement",
				expected_text: "old",
				range: {
					start_line: 0,
					start_column: 1,
					end_line: 1,
					end_column: 1,
				},
			},
		}),
		false,
	);
});

test("cache keys include both sides of the recent edit", () => {
	const base = tabCacheKey("main.go", "after", "before", 2, 4);
	assert.equal(base, tabCacheKey("main.go", "after", "before", 2, 4));
	assert.notEqual(base, tabCacheKey("main.go", "after!", "before", 2, 4));
	assert.notEqual(base, tabCacheKey("main.go", "after", "before!", 2, 4));
	assert.notEqual(base, tabCacheKey("main.go", "after", "before", 3, 4));
});

test("recognizes synchronized whole-document replacements", () => {
	assert.equal(
		replacesEntireTabDocument(
			{ isFlush: false, changes: [{ rangeOffset: 0, rangeLength: 12 }] },
			12,
		),
		true,
	);
	assert.equal(
		replacesEntireTabDocument(
			{ isFlush: false, changes: [{ rangeOffset: 3, rangeLength: 2 }] },
			12,
		),
		false,
	);
});
