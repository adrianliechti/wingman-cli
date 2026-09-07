import {
	APIError,
	createWorkspaceHTTP,
	setWorkspaceHTTP,
} from "../api/http.ts";
import type { ServerMessage } from "../types/protocol.ts";
import {
	SessionStore,
	sessionKey,
	splitSessionKey,
	type SessionEvent,
	type SessionRef,
	type WorkspaceScope,
} from "./sessionStore.ts";

export type Bootstrap = WorkspaceScope & {
	protocol: number;
	backends: { id: string; name: string }[];
};
export async function fetchBootstrap(): Promise<Bootstrap> {
	const response = await fetch("/api/v2/bootstrap");
	if (!response.ok) throw new APIError(await response.text(), response.status);
	const scope = (await response.json()) as Bootstrap;
	if (
		scope.protocol !== 2 ||
		!scope.instanceId ||
		!scope.workspaceId ||
		!Array.isArray(scope.backends)
	)
		throw new Error("Unsupported workspace protocol. Reload the application.");
	return scope;
}

export type Receipt = {
	id: string;
	ref: SessionRef;
	epoch: string;
	outcome: string;
};
export type Command = {
	type: string;
	id?: string;
	epoch?: string;
	inputId?: string;
	[key: string]: unknown;
};
export function sessionPath(key: string): string {
	const ref = splitSessionKey(key);
	return `/api/v2/backends/${encodeURIComponent(ref.backendId)}/sessions/${encodeURIComponent(ref.sessionId)}`;
}

export class WorkspaceClient {
	readonly store = new SessionStore();
	readonly scope: Bootstrap;
	readonly http: ReturnType<typeof createWorkspaceHTTP>;
	private generation = 0;
	private ws: WebSocket | null = null;
	private timer: ReturnType<typeof setTimeout> | undefined;
	private stopped = true;
	private observed = new Map<string, { count: number; id: string }>();
	private listeners = new Set<(event: ServerMessage) => void>();
	private connectionListeners = new Set<() => void>();
	private connected = false;
	private replaced = false;
	readonly getReplaced = () => this.replaced;
	private sends = new Map<
		string,
		{ command: Command; pending?: Promise<Receipt> }
	>();
	private creations = new Map<
		string,
		{ id: string; pending?: Promise<string> }
	>();
	constructor(scope: Bootstrap) {
		this.scope = scope;
		this.http = createWorkspaceHTTP(scope.instanceId);
	}
	readonly getConnected = () => this.connected;
	readonly subscribeConnection = (listener: () => void) => {
		this.connectionListeners.add(listener);
		return () => {
			this.connectionListeners.delete(listener);
		};
	};
	readonly subscribe = (listener: (event: ServerMessage) => void) => {
		this.listeners.add(listener);
		return () => {
			this.listeners.delete(listener);
		};
	};
	private setConnected(value: boolean) {
		this.connected = value;
		for (const fn of this.connectionListeners) fn();
	}
	start() {
		if (!this.stopped || this.replaced) return;
		this.stopped = false;
		this.connect();
	}
	stop() {
		this.generation++;
		this.stopped = true;
		clearTimeout(this.timer);
		this.ws?.close();
		this.ws = null;
		this.setConnected(false);
		this.store.disconnect();
	}
	private connect() {
		if (this.stopped || this.replaced) return;
		const generation = ++this.generation;
		const protocol = location.protocol === "https:" ? "wss:" : "ws:";
		const ws = new WebSocket(
			`${protocol}//${location.host}/api/v2/events?instance=${encodeURIComponent(this.scope.instanceId)}`,
		);
		this.ws = ws;
		ws.onopen = () => {
			if (this.ws !== ws) return;
			this.setConnected(true);
			for (const key of this.observed.keys()) this.resubscribe(key);
		};
		ws.onmessage = (event) => {
			if (this.ws !== ws) return;
			let message: SessionEvent | ServerMessage;
			try {
				message = JSON.parse(event.data);
			} catch {
				return;
			}
			if (message.type.startsWith("session.")) {
				const update = message as SessionEvent;
				if (update.ref.workspaceId !== this.scope.workspaceId) return;
				if (this.store.apply(update) === "resubscribe")
					this.resubscribe(
						sessionKey(update.ref.backendId, update.ref.sessionId),
					);
			} else {
				for (const listener of this.listeners)
					listener(message as ServerMessage);
			}
		};
		ws.onclose = () => {
			if (this.ws !== ws || this.stopped) return;
			this.ws = null;
			this.setConnected(false);
			this.store.disconnect();
			// The same native-window origin can now point at a different workspace.
			void fetchBootstrap()
				.then((scope) => {
					if (this.stopped || generation !== this.generation) return;
					if (scope.instanceId !== this.scope.instanceId) {
						this.replaced = true;
						this.setConnected(false);
						return;
					}
					this.timer = setTimeout(() => this.connect(), 1000);
				})
				.catch(() => {
					if (!this.stopped && generation === this.generation)
						this.timer = setTimeout(() => this.connect(), 2000);
				});
		};
	}
	private control(message: object) {
		if (this.ws?.readyState === WebSocket.OPEN)
			this.ws.send(JSON.stringify(message));
	}
	private resubscribe(key: string) {
		const subscription = this.observed.get(key);
		if (!subscription) return;
		this.control({ type: "unsubscribe", subscriptionId: subscription.id });
		subscription.id = crypto.randomUUID();
		this.store.expect(key, subscription.id);
		this.control({
			type: "subscribe",
			subscriptionId: subscription.id,
			ref: { workspaceId: this.scope.workspaceId, ...splitSessionKey(key) },
		});
	}
	watch(key: string): () => void {
		if (!splitSessionKey(key).sessionId) return () => {};
		const existing = this.observed.get(key);
		if (existing) existing.count++;
		else {
			this.observed.set(key, { count: 1, id: "" });
			this.resubscribe(key);
		}
		const entry = this.observed.get(key)!;
		let released = false;
		return () => {
			if (released) return;
			released = true;
			if (this.observed.get(key) !== entry || --entry.count > 0) return;
			this.control({ type: "unsubscribe", subscriptionId: entry.id });
			this.observed.delete(key);
			this.store.forget(key);
		};
	}
	private assertActive() {
		if (this.replaced)
			throw new Error("Workspace changed. Reload to reconnect.");
		if (this.stopped) throw new Error("Workspace client stopped.");
	}
	private async ready(key: string) {
		this.assertActive();
		const release = this.watch(key);
		try {
			return await new Promise<string>((resolve, reject) => {
				let stopStore = () => {},
					stopConnection = () => {};
				const finish = (error?: Error, epoch?: string) => {
					clearTimeout(timer);
					stopStore();
					stopConnection();
					if (error) reject(error);
					else resolve(epoch!);
				};
				const timer = setTimeout(
					() =>
						finish(
							new Error("Waiting for session synchronization. Please retry."),
						),
					30000,
				);
				const check = () => {
					try {
						this.assertActive();
					} catch (error) {
						finish(error as Error);
						return;
					}
					const view = this.store.getSnapshot()[key];
					if (!view) return;
					if (view.status === "error" || view.status === "deleted") {
						finish(new Error(view.error ?? "Session deleted"));
						return;
					}
					if (view.synchronized && view.status === "ready")
						finish(undefined, view.epoch);
				};
				stopStore = this.store.subscribe(check);
				stopConnection = this.subscribeConnection(check);
				check();
			});
		} finally {
			release();
		}
	}

	async command(key: string, command: Command): Promise<Receipt> {
		this.assertActive();
		const epoch =
			command.epoch ??
			(command.type === "prompt_response"
				? this.store.getSnapshot()[key]?.epoch
				: await this.ready(key));
		this.assertActive();
		return this.http.fetchJSON<Receipt>(`${sessionPath(key)}/commands`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({
				...command,
				epoch,
				id: command.id ?? crypto.randomUUID(),
			}),
		});
	}
	async send(
		key: string,
		text: string,
		files: string[] = [],
		images: string[] = [],
		intent = "follow_up",
	) {
		this.assertActive();
		// Capture the envelope before any await; a composer may replace its arrays.
		files = [...files];
		images = [...images];
		// An unconfirmed delivery keeps its original intent as part of the saved
		// command. The session phase may advance before the user retries, so intent
		// must not split the retry identity and allocate a second request ID.
		const fingerprint = JSON.stringify([key, text, files, images]);
		let entry = this.sends.get(fingerprint);
		if (!entry) {
			const epoch = await this.ready(key);
			entry = this.sends.get(fingerprint) ?? {
				command: {
					type: "send",
					id: crypto.randomUUID(),
					epoch,
					text,
					files,
					images,
					intent,
				},
			};
			this.sends.set(fingerprint, entry);
		}
		if (!entry.pending) {
			const current = entry;
			current.pending = this.command(key, current.command)
				.then((receipt) => {
					if (this.sends.get(fingerprint) === current)
						this.sends.delete(fingerprint);
					return receipt;
				})
				.catch((error) => {
					if (
						error instanceof APIError &&
						error.status < 500 &&
						this.sends.get(fingerprint) === current
					)
						this.sends.delete(fingerprint);
					throw error;
				})
				.finally(() => {
					current.pending = undefined;
				});
		}
		await entry.pending;
	}
	create(backend: string, draftID: string): Promise<string> {
		this.assertActive();
		const key = JSON.stringify([backend, draftID]);
		let entry = this.creations.get(key);
		if (!entry) {
			entry = { id: crypto.randomUUID() };
			this.creations.set(key, entry);
		}
		if (!entry.pending) {
			const current = entry;
			current.pending = this.http
				.fetchJSON<Receipt>(
					`/api/v2/backends/${encodeURIComponent(backend)}/sessions`,
					{
						method: "POST",
						headers: { "Content-Type": "application/json" },
						body: JSON.stringify({ id: current.id, type: "create" }),
					},
				)
				.then((receipt) =>
					sessionKey(receipt.ref.backendId, receipt.ref.sessionId),
				)
				.catch((error) => {
					current.pending = undefined;
					throw error;
				});
		}
		return entry.pending!;
	}

	focus() {
		this.control({ type: "focus" });
	}
}

let client: WorkspaceClient;
export function initializeWorkspace(scope: Bootstrap) {
	client?.stop();
	client = new WorkspaceClient(scope);
	setWorkspaceHTTP(client.http);
	return client;
}
export function workspaceClient(): WorkspaceClient {
	return client;
}
