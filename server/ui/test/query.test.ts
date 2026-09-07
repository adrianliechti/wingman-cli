import assert from "node:assert/strict";
import test from "node:test";
import { QueryClient } from "@tanstack/react-query";
import {
	invalidateAllServerQueries,
	invalidateForServerMessage,
	queryKeys,
	createServerQueryClient,
} from "../src/api/query.ts";

test("server defaults keep fresh data quiet and stale data recoverable", () => {
	const defaults = createServerQueryClient().getDefaultOptions();

	assert.equal(defaults.queries?.staleTime, Infinity);
	assert.equal(defaults.queries?.retry, false);
	assert.equal(defaults.queries?.refetchOnWindowFocus, undefined);
	assert.equal(defaults.queries?.refetchOnReconnect, undefined);
	assert.equal(defaults.mutations, undefined);
});

test("skills events invalidate every session-scoped skill catalog", () => {
	const client = new QueryClient();
	const first = queryKeys.skills.list("one");
	const second = queryKeys.skills.list("two");
	client.setQueryData(first, []);
	client.setQueryData(second, []);

	invalidateForServerMessage(client, { type: "skills_changed" });

	assert.equal(client.getQueryState(first)?.isInvalidated, true);
	assert.equal(client.getQueryState(second)?.isInvalidated, true);
});

test("diff events invalidate diff and Git query families", () => {
	const client = new QueryClient();
	const diff = queryKeys.diffs.list();
	const history = queryKeys.git.history;
	client.setQueryData(diff, []);
	client.setQueryData(history, []);

	invalidateForServerMessage(client, { type: "diffs_changed" });

	assert.equal(client.getQueryState(diff)?.isInvalidated, true);
	assert.equal(client.getQueryState(history)?.isInvalidated, true);
});

test("Git index events refresh status before dependent diff views", async () => {
	const client = new QueryClient();
	const status = queryKeys.git.status;
	const diff = queryKeys.diffs.list("staged", "file.txt");
	const comparison = queryKeys.git.compare("main", ":worktree", "merge-base");
	const committedComparison = queryKeys.git.compare(
		"main",
		"feature",
		"direct",
	);
	const history = queryKeys.git.history;
	client.setQueryData(status, {});
	client.setQueryData(diff, []);
	client.setQueryData(comparison, {});
	client.setQueryData(committedComparison, {});
	client.setQueryData(history, []);

	invalidateForServerMessage(client, { type: "git_index_changed" });
	await new Promise((resolve) => setImmediate(resolve));

	assert.equal(client.getQueryState(status)?.isInvalidated, true);
	assert.equal(client.getQueryState(diff)?.isInvalidated, true);
	assert.equal(client.getQueryState(comparison)?.isInvalidated, true);
	assert.equal(client.getQueryState(committedComparison)?.isInvalidated, false);
	assert.equal(client.getQueryState(history)?.isInvalidated, false);
});

test("task events only invalidate the affected session when identified", () => {
	const client = new QueryClient();
	const first = queryKeys.tasks.list(JSON.stringify(["wingman", "one"]));
	const second = queryKeys.tasks.list("two");
	client.setQueryData(first, []);
	client.setQueryData(second, []);

	invalidateForServerMessage(client, {
		type: "tasks_changed",
		session: "one",
	});

	assert.equal(client.getQueryState(first)?.isInvalidated, true);
	assert.equal(client.getQueryState(second)?.isInvalidated, false);
});

test("reconnect invalidation covers every server query family", () => {
	const client = new QueryClient();
	const keys = [
		["server", "backend-settings", "wingman"],
		queryKeys.terminals.list,
		queryKeys.tasks.list("session"),
	] as const;
	for (const key of keys) client.setQueryData(key, {});

	invalidateAllServerQueries(client);

	for (const key of keys) {
		assert.equal(client.getQueryState(key)?.isInvalidated, true);
	}
});

test("file events refresh graph status without reloading indexed graph data", () => {
	const client = new QueryClient();
	client.setQueryData(queryKeys.files.tree(""), []);
	client.setQueryData(queryKeys.insights.overview, {});
	client.setQueryData(queryKeys.insights.modules, {});

	invalidateForServerMessage(client, { type: "files_changed" });

	assert.equal(
		client.getQueryState(queryKeys.files.tree(""))?.isInvalidated,
		true,
	);
	assert.equal(
		client.getQueryState(queryKeys.insights.overview)?.isInvalidated,
		true,
	);
	assert.equal(
		client.getQueryState(queryKeys.insights.modules)?.isInvalidated,
		false,
	);
});
