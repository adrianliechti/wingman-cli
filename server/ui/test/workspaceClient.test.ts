import assert from "node:assert/strict";
import test, { type TestContext } from "node:test";
import { setImmediate } from "node:timers/promises";
import {
	initializeWorkspace,
	WorkspaceClient,
	type Bootstrap,
} from "../src/state/workspaceClient.ts";
import { createWorkspaceHTTP } from "../src/api/http.ts";
import { emptySession, sessionKey } from "../src/state/sessionStore.ts";

const scope: Bootstrap = {
	workspaceId: "workspace",
	instanceId: "instance",
	protocol: 2,
	backends: [
		{ id: "one", name: "One" },
		{ id: "two", name: "Two" },
	],
};
const key = sessionKey("one", "saved");
function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((yes, no) => {
		resolve = yes;
		reject = no;
	});
	return { promise, resolve, reject };
}
class Socket {
	static OPEN = 1;
	static instances: Socket[] = [];
	readyState = 0;
	onopen?: () => void;
	onclose?: () => void;
	onmessage?: (event: { data: string }) => void;
	sent: Record<string, any>[] = [];
	readonly url: string;
	constructor(url: string) {
		this.url = url;
		Socket.instances.push(this);
	}
	send(message: string) {
		this.sent.push(JSON.parse(message));
	}
	open() {
		this.readyState = 1;
		this.onopen?.();
	}
	close() {
		this.readyState = 3;
		this.onclose?.();
	}
	receive(message: unknown) {
		this.onmessage?.({ data: JSON.stringify(message) });
	}
}
function setup(t: TestContext, fetcher: typeof fetch) {
	Socket.instances = [];
	for (const [name, value] of Object.entries({
		location: { protocol: "http:", host: "workspace.test" },
		WebSocket: Socket,
	})) {
		const previous = Object.getOwnPropertyDescriptor(globalThis, name);
		Object.defineProperty(globalThis, name, { value, configurable: true });
		t.after(() => {
			if (previous) Object.defineProperty(globalThis, name, previous);
			else Reflect.deleteProperty(globalThis, name);
		});
	}
	t.mock.method(globalThis, "fetch", fetcher);
	t.mock.timers.enable({ apis: ["setTimeout"] });
	const client = initializeWorkspace(scope);
	client.start();
	t.after(() => {
		client.stop();
		t.mock.timers.tick(30000);
	});
	return client;
}
function ready(client: WorkspaceClient) {
	client.watch(key);
	const socket = Socket.instances.at(-1)!;
	socket.open();
	const subscription = socket.sent.findLast(
		(message) => message.type === "subscribe",
	)!;
	socket.receive({
		type: "session.snapshot",
		ref: subscription.ref,
		subscriptionId: subscription.subscriptionId,
		epoch: "epoch",
		revision: 0,
		entries: [],
		state: { ...emptySession(key), status: "ready" },
	});
	return socket;
}

test("concurrent sends share one HTTP attempt and one outcome", async (t) => {
	const response = deferred<Response>();
	let calls = 0;
	const client = setup(t, async () => {
		calls++;
		return response.promise;
	});
	ready(client);
	const a = client.send(key, "hello"),
		b = client.send(key, "hello");
	await setImmediate();
	assert.equal(
		calls,
		1,
		"concurrent callers must not get contradictory receipts",
	);
	response.resolve(Response.json({ outcome: "accepted" }));
	await Promise.all([a, b]);
});

test("uncertain retries preserve the request ID and copy the submitted envelope", async (t) => {
	const commands: any[] = [];
	const client = setup(t, async (_url, init) => {
		commands.push(JSON.parse(String(init?.body)));
		if (commands.length === 1) throw new TypeError("response lost");
		return Response.json({ outcome: "accepted" });
	});
	ready(client);
	const files = ["one.go"];
	const first = client.send(key, "hello", files);
	files.push("unrelated.go");
	await assert.rejects(first, /response lost/);
	await client.send(key, "hello", ["one.go"]);
	assert.equal(commands[0].id, commands[1].id);
	assert.deepEqual(commands[0].files, ["one.go"]);
	assert.deepEqual(commands[0], commands[1]);
});

test("draft allocation identity includes the backend", async (t) => {
	const client = setup(t, async (url) => {
		const backend = String(url).split("/")[4];
		return Response.json({
			ref: { ...scope, backendId: backend, sessionId: "native" },
		});
	});
	const one = await client.create("one", "draft");
	const two = await client.create("two", "draft");
	assert.equal(one, sessionKey("one", "native"));
	assert.equal(two, sessionKey("two", "native"));
});

test("a stale bootstrap response cannot replace a restarted connection", async (t) => {
	const bootstrap = deferred<Response>();
	const client = setup(t, () => bootstrap.promise);
	const old = ready(client);
	old.close();
	client.stop();
	client.start();
	Socket.instances.at(-1)!.open();
	bootstrap.resolve(
		Response.json({ ...scope, instanceId: "obsolete-response" }),
	);
	await setImmediate();
	t.mock.timers.tick(5000);
	assert.equal(client.getReplaced(), false);
	assert.equal(client.getConnected(), true);
	assert.equal(Socket.instances.length, 2);
});

test("temporary bootstrap failure retries without claiming the workspace changed", async (t) => {
	const client = setup(t, async () =>
		Response.json({ error: "temporarily unavailable" }, { status: 503 }),
	);
	ready(client).close();
	await setImmediate();
	assert.equal(client.getReplaced(), false);
	t.mock.timers.tick(2000);
	assert.equal(Socket.instances.length, 2);
});

test("stopping the client promptly rejects commands waiting for a snapshot", async (t) => {
	const client = setup(t, async () => {
		throw new Error("unexpected HTTP command");
	});
	let error: unknown;
	const command = client.command(key, { type: "cancel" }).catch((reason) => {
		error = reason;
	});
	client.stop();
	await setImmediate();
	assert.match(String(error), /stopped|closed|disconnected/i);
	await command;
});

test("releasing one observer twice cannot remove another observer", async (t) => {
	const client = setup(t, async () => Response.json(scope));
	const a = client.watch(key),
		b = client.watch(key);
	const socket = Socket.instances[0];
	socket.open();
	const before = socket.sent.length;
	a();
	a();
	assert.equal(socket.sent.length, before);
	b();
	assert.equal(socket.sent.length, before + 1);
	assert.equal(socket.sent.at(-1)?.type, "unsubscribe");
});

test("independent clients retain their own HTTP instance identity", async (t) => {
	const instances: string[] = [];
	const first = setup(t, async (_url, init) => {
		instances.push(new Headers(init?.headers).get("X-Wingman-Instance")!);
		return Response.json({ outcome: "accepted" });
	});
	const second = new WorkspaceClient({
		...scope,
		instanceId: "second-instance",
	});
	second.start();
	t.after(() => second.stop());
	await first.command(key, { type: "cancel", epoch: "first-epoch" });
	await second.command(key, { type: "cancel", epoch: "second-epoch" });
	assert.deepEqual(instances, ["instance", "second-instance"]);
});

test("scoping a Request preserves its body and headers", async (t) => {
	let received: Request | undefined;
	setup(t, async (input, init) => {
		received = new Request(input, init);
		return Response.json({});
	});
	const transport = createWorkspaceHTTP("bound-instance");
	await transport.scopedFetch(
		new Request("https://workspace.test/api/example", {
			method: "POST",
			headers: { "Content-Type": "application/json", "X-Custom": "kept" },
			body: '{"value":1}',
		}),
	);
	assert.equal(received!.headers.get("X-Custom"), "kept");
	assert.equal(received!.headers.get("X-Wingman-Instance"), "bound-instance");
	assert.equal(await received!.text(), '{"value":1}');
});
