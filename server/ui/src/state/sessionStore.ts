import type {
	ChatEntry,
	PendingPrompt,
	PendingTurnInput,
} from "../types/session.ts";
import type { Phase } from "../types/protocol.ts";
import type { ModelInfo } from "../api/models.ts";
import type { ModeOption } from "../api/sessions.ts";

export type WorkspaceScope = { workspaceId: string; instanceId: string };
export type SessionRef = {
	workspaceId: string;
	backendId: string;
	sessionId: string;
};
export type SessionSettings = {
	models: ModelInfo[];
	model: string;
	effort: string;
	efforts: string[];
	modes: ModeOption[];
	mode: string;
	canDelete: boolean;
};
export type SessionFields = {
	status: "loading" | "ready" | "error" | "deleted";
	phase: Phase;
	error: string | null;
	usage: {
		inputTokens: number;
		cachedTokens: number;
		outputTokens: number;
		lastInputTokens: number;
		contextWindow: number;
	};
	prompts: PendingPrompt[];
	pendingInputs: PendingTurnInput[];
	queuePaused: boolean;
	canSteer: boolean;
	settings: SessionSettings;
	toolProgress: Record<string, string>;
};
export type SessionView = SessionFields & {
	id: string;
	entries: ChatEntry[];
	epoch: string;
	revision: number;
	synchronized: boolean;
};
export type SessionChange =
	| { type: "entries.replace"; entries?: ChatEntry[] }
	| { type: "entry.upsert"; entry: ChatEntry }
	| { type: "entry.append"; id: string; text: string }
	| { type: "entries.remove"; ids: string[] }
	| { type: "state.replace"; state: SessionFields };
export type SessionEvent = {
	type: "session.snapshot" | "session.update" | "session.error";
	subscriptionId: string;
	ref: SessionRef;
	epoch: string;
	revision: number;
	previousRevision: number;
	state?: SessionFields;
	entries?: ChatEntry[];
	changes?: SessionChange[];
	message?: string;
};

export function sessionKey(backendId: string, sessionId: string): string {
	return JSON.stringify([backendId, sessionId]);
}
export function splitSessionKey(key: string): {
	backendId: string;
	sessionId: string;
} {
	if (!key) return { backendId: "wingman", sessionId: "" };
	const [backendId, sessionId] = JSON.parse(key) as [string, string];
	return { backendId, sessionId };
}
export function isDraft(key: string): boolean {
	return !splitSessionKey(key).sessionId;
}

export const EMPTY_SETTINGS: SessionSettings = {
	models: [],
	model: "",
	effort: "",
	efforts: [],
	modes: [],
	mode: "",
	canDelete: false,
};
export function emptySession(id: string): SessionView {
	return {
		id,
		epoch: "",
		revision: 0,
		synchronized: false,
		status: "loading",
		phase: "idle",
		entries: [],
		error: null,
		usage: {
			inputTokens: 0,
			cachedTokens: 0,
			outputTokens: 0,
			lastInputTokens: 0,
			contextWindow: 0,
		},
		prompts: [],
		pendingInputs: [],
		queuePaused: false,
		canSteer: false,
		settings: EMPTY_SETTINGS,
		toolProgress: {},
	};
}
function normalize(fields: SessionFields): SessionFields {
	return {
		// The event envelope alone owns identity, revision, and transcript.
		status: fields.status,
		phase: fields.phase,
		error: fields.error,
		usage: fields.usage,
		prompts: fields.prompts,
		queuePaused: fields.queuePaused,
		canSteer: fields.canSteer,
		settings: fields.settings,
		toolProgress: fields.toolProgress,
		pendingInputs: fields.pendingInputs
			.filter((input) => input.origin !== "task")
			.map((input) => ({
				...input,
				text: input.text ?? "",
				files: input.files ?? [],
				images: input.images ?? [],
				position: input.position ?? 0,
			})),
	};
}

// The only browser owner of authoritative session state. It applies explicit
// changes and validates ordering; it never reconstructs provider history.
export class SessionStore {
	private views: Record<string, SessionView> = {};
	private subscriptions = new Map<string, string>();
	private listeners = new Set<() => void>();
	readonly getSnapshot = () => this.views;
	readonly subscribe = (listener: () => void) => {
		this.listeners.add(listener);
		return () => {
			this.listeners.delete(listener);
		};
	};
	private emit() {
		for (const listener of this.listeners) listener();
	}
	expect(key: string, subscriptionId: string) {
		this.subscriptions.set(key, subscriptionId);
		const previous = this.views[key] ?? emptySession(key);
		this.views = {
			...this.views,
			[key]: {
				...previous,
				synchronized: false,
				...(previous.status === "error"
					? ({ status: "loading", error: null } as const)
					: {}),
			},
		};
		this.emit();
	}
	forget(key: string) {
		this.subscriptions.delete(key);
	}
	disconnect() {
		this.views = Object.fromEntries(
			Object.entries(this.views).map(([key, view]) => [
				key,
				{ ...view, synchronized: false },
			]),
		);
		this.emit();
	}
	apply(event: SessionEvent): "applied" | "ignored" | "resubscribe" {
		const key = sessionKey(event.ref.backendId, event.ref.sessionId);
		if (this.subscriptions.get(key) !== event.subscriptionId) return "ignored";
		const previous = this.views[key] ?? emptySession(key);
		let next: SessionView;
		if (event.type === "session.error") {
			next = {
				...previous,
				status: "error",
				error: event.message ?? "Could not load session",
				synchronized: false,
			};
		} else if (event.type === "session.snapshot" && event.state) {
			if (
				previous.synchronized &&
				event.epoch === previous.epoch &&
				event.revision <= previous.revision
			)
				return "ignored";
			next = {
				...normalize(event.state),
				id: key,
				entries: event.entries ?? [],
				epoch: event.epoch,
				revision: event.revision,
				synchronized: true,
			};
		} else {
			if (!previous.synchronized) return "ignored";
			if (event.epoch !== previous.epoch) return "resubscribe";
			if (event.revision <= previous.revision) return "ignored";
			if (
				event.previousRevision !== previous.revision ||
				event.revision !== previous.revision + 1
			)
				return "resubscribe";
			next = { ...previous, revision: event.revision };
			for (const change of event.changes ?? []) {
				switch (change.type) {
					case "entries.replace":
						next.entries = change.entries ?? [];
						break;
					case "entry.upsert": {
						const index = next.entries.findIndex(
							(entry) => entry.id === change.entry.id,
						);
						next.entries = [...next.entries];
						if (index < 0) next.entries.push(change.entry);
						else next.entries[index] = change.entry;
						break;
					}
					case "entry.append":
						if (!next.entries.some((entry) => entry.id === change.id))
							return "resubscribe";
						next.entries = next.entries.map((entry) =>
							entry.id === change.id
								? { ...entry, content: entry.content + change.text }
								: entry,
						);
						break;
					case "entries.remove": {
						const ids = new Set(change.ids);
						next.entries = next.entries.filter((entry) => !ids.has(entry.id));
						break;
					}
					case "state.replace":
						next = { ...next, ...normalize(change.state) };
						break;
				}
			}
		}
		this.views = { ...this.views, [key]: next };
		this.emit();
		return "applied";
	}
}
