import assert from "node:assert/strict";
import test from "node:test";
import {
	generateDebugPlan,
	type DebugLaunchPlan,
	type DebugToolProgress,
	type DebugToolStatus,
} from "../src/api/debug.ts";

const request = {
	action: "debug" as const,
	target_id: "go:main",
	current_path: "main.go",
};
const plan: DebugLaunchPlan = {
	action: "debug",
	title: "Debug main",
	adapter: "delve",
	terminal_available: true,
	project_dir: ".",
	request: "launch",
	io: "output",
	configuration: { program: "." },
	breakpoints: [],
	function_breakpoints: [],
};
const progress: DebugToolProgress = {
	tool: "delve",
	label: "Go debugger",
	phase: "installing",
	current: 1,
	total: 1,
};

const tools: DebugToolStatus[] = [
	{ tool: "delve", label: "Go debugger", installed: false, installable: true },
];

test("debug preparation asks for installation without approving a download", async (t) => {
	let calls = 0;
	t.mock.method(
		globalThis,
		"fetch",
		async (_url: string, init: RequestInit) => {
			calls++;
			assert.notEqual(JSON.parse(init.body as string).install, true);
			return new Response(
				JSON.stringify({ type: "installation_required", tools }) + "\n",
			);
		},
	);
	assert.deepEqual(await generateDebugPlan(request), {
		type: "installation_required",
		tools,
	});
	assert.equal(calls, 1);
});

test("debug preparation streams progress before the plan across split chunks", async (t) => {
	let stream!: ReadableStreamDefaultController<Uint8Array>;
	const response = new Response(
		new ReadableStream<Uint8Array>({
			start(controller) {
				stream = controller;
			},
		}),
	);
	const controller = new AbortController();
	t.mock.method(globalThis, "fetch", async (url: string, init: RequestInit) => {
		assert.equal(url, "/api/debug/plan");
		assert.equal(
			new Headers(init.headers).get("Accept"),
			"application/x-ndjson",
		);
		assert.deepEqual(JSON.parse(init.body as string), {
			...request,
			install: true,
		});
		assert.equal(init.signal, controller.signal);
		return response;
	});
	const observed: DebugToolProgress[] = [];
	const reported = Promise.withResolvers<void>();
	const toolStates: DebugToolStatus[][] = [];
	const prepared = generateDebugPlan(
		{ ...request, install: true },
		controller.signal,
		(update) => {
			observed.push(update);
			reported.resolve();
		},
		(statuses) => toolStates.push(statuses),
	);
	const encoder = new TextEncoder();
	const event = JSON.stringify({ type: "progress", progress }) + "\n";
	stream.enqueue(encoder.encode(event.slice(0, 20)));
	stream.enqueue(encoder.encode(event.slice(20)));
	await reported.promise;
	assert.deepEqual(observed, [progress]);
	const installed = tools.map((tool) => ({ ...tool, installed: true }));
	stream.enqueue(
		encoder.encode(JSON.stringify({ type: "tools", tools: installed }) + "\n"),
	);
	stream.enqueue(encoder.encode(JSON.stringify({ type: "plan", plan })));
	stream.close();
	assert.deepEqual(await prepared, { type: "plan", plan });
	assert.deepEqual(toolStates, [installed]);
});

test("debug preparation surfaces installation failures", async (t) => {
	t.mock.method(
		globalThis,
		"fetch",
		async () =>
			new Response(
				JSON.stringify({ type: "progress", progress }) +
					"\n" +
					JSON.stringify({ type: "error", error: "Go is not installed" }) +
					"\n",
			),
	);
	await assert.rejects(generateDebugPlan(request), /Go is not installed/);
});

test("debug preparation rejects an incomplete stream", async (t) => {
	t.mock.method(
		globalThis,
		"fetch",
		async () =>
			new Response(JSON.stringify({ type: "progress", progress }) + "\n"),
	);
	await assert.rejects(
		generateDebugPlan(request),
		/ended before a plan was ready/,
	);
});

test("debug preparation stops reporting progress after cancellation", async (t) => {
	const controller = new AbortController();
	t.mock.method(globalThis, "fetch", async () => {
		controller.abort();
		return new Response(JSON.stringify({ type: "progress", progress }) + "\n");
	});
	const observed: DebugToolProgress[] = [];
	await assert.rejects(
		generateDebugPlan(request, controller.signal, (update) =>
			observed.push(update),
		),
		{ name: "AbortError" },
	);
	assert.deepEqual(observed, []);
});

test("debug preparation stops within a buffered batch when cancelled", async (t) => {
	const controller = new AbortController();
	t.mock.method(
		globalThis,
		"fetch",
		async () =>
			new Response(
				[
					{ type: "progress", progress },
					{ type: "progress", progress: { ...progress, phase: "checking" } },
					{ type: "plan", plan },
				]
					.map((event) => JSON.stringify(event))
					.join("\n"),
			),
	);
	const observed: DebugToolProgress[] = [];
	await assert.rejects(
		generateDebugPlan(request, controller.signal, (update) => {
			observed.push(update);
			controller.abort();
		}),
		{ name: "AbortError" },
	);
	assert.deepEqual(observed, [progress]);
});
