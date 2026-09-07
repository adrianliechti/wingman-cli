import type { CompareMode, DiffLayer } from "./types/protocol";
import type { TabDisposition } from "./types/tabs";

export interface CenterTab {
	id: string;
	type:
		| "chat"
		| "file"
		| "diff"
		| "compare"
		| "terminal"
		| "debug"
		| "task"
		| "graph";
	label: string;
	path?: string;
	diffLayer?: DiffLayer;
	compareBase?: string;
	compareHead?: string;
	compareMode?: CompareMode;
	line?: number;
	column?: number;
	navigationKey?: number;
	external?: boolean;
	sessionId?: string;
	backendId?: string;
	terminalId?: string;
	taskId?: string;
	preview?: boolean;
	pane?: "right";
}

export type PaneSide = "left" | "right";

export function paneOf(tab: CenterTab): PaneSide {
	return tab.pane === "right" ? "right" : "left";
}

export const chatTabId = (sessionId: string) => `chat:${sessionId}`;

export function draftChatTab(
	backendId = "wingman",
	nonce: string = crypto.randomUUID(),
): CenterTab {
	return {
		id: `draft:${backendId}:${nonce}`,
		type: "chat",
		label: "Agent",
		backendId,
		sessionId: "",
	};
}

export function placeCenterTab(
	current: CenterTab[],
	candidate: CenterTab,
	disposition: TabDisposition,
	dirtyPaths: ReadonlySet<string>,
): { tabs: CenterTab[]; replaced?: CenterTab } {
	const existingIndex = current.findIndex((tab) => tab.id === candidate.id);
	if (existingIndex >= 0) {
		const existing = current[existingIndex];
		if (disposition !== "keep" || !existing.preview) return { tabs: current };
		const tabs = [...current];
		tabs[existingIndex] = { ...existing, preview: undefined };
		return { tabs };
	}

	const placed: CenterTab = {
		...candidate,
		preview: disposition === "preview" || undefined,
	};
	if (disposition === "keep") return { tabs: [...current, placed] };

	const previewIndex = current.findIndex(
		(tab) => tab.preview && paneOf(tab) === paneOf(placed),
	);
	if (previewIndex < 0) return { tabs: [...current, placed] };
	const previous = current[previewIndex];
	if (
		previous.type === "file" &&
		previous.path &&
		dirtyPaths.has(previous.path)
	) {
		const tabs = current.map((tab, index) =>
			index === previewIndex ? { ...tab, preview: undefined } : tab,
		);
		return { tabs: [...tabs, placed] };
	}
	const tabs = [...current];
	tabs[previewIndex] = placed;
	return { tabs, replaced: previous };
}

export function syncDebugTab(
	current: CenterTab[],
	terminalId: string | undefined,
	ensure: boolean,
	pane?: "right",
): CenterTab[] {
	let changed = false;
	let next = current;
	if (terminalId) {
		const filtered = current.filter(
			(tab) => tab.type !== "terminal" || tab.terminalId !== terminalId,
		);
		if (filtered.length !== current.length) {
			next = filtered;
			changed = true;
		}
	}

	const index = next.findIndex((tab) => tab.id === "debug");
	if (index < 0) {
		return ensure
			? [
					...next,
					{ id: "debug", type: "debug", label: "Debug", terminalId, pane },
				]
			: next;
	}
	if (next[index].terminalId === terminalId) return changed ? next : current;

	const updated = [...next];
	updated[index] = { ...updated[index], terminalId };
	return updated;
}

// Reorders a tab to the given strip position; the target index is expressed
// against the pre-removal list, as drop indicators report it.
export function moveTab(
	current: CenterTab[],
	tabId: string,
	index: number,
): CenterTab[] {
	const from = current.findIndex((tab) => tab.id === tabId);
	if (from < 0) return current;
	let to = Math.max(0, Math.min(index, current.length));
	if (from < to) to -= 1;
	if (to === from) return current;
	const next = [...current];
	const [tab] = next.splice(from, 1);
	next.splice(to, 0, tab);
	return next;
}

export type LayoutState = {
	tabs: CenterTab[];
	activeTabId: string;
	leftActiveId: string;
	rightActiveId: string;
	currentSessionId: string;
};
export type LayoutAction = {
	[K in keyof LayoutState]: {
		field: K;
		value: LayoutState[K] | ((previous: LayoutState[K]) => LayoutState[K]);
	} & (K extends "tabs" ? { fallbackId: string } : object);
}[keyof LayoutState];
export function layoutReducer(
	state: LayoutState,
	action: LayoutAction,
): LayoutState {
	const value =
		typeof action.value === "function"
			? (action.value as (previous: unknown) => unknown)(state[action.field])
			: action.value;
	if (value === state[action.field]) return state;
	const next = { ...state, [action.field]: value } as LayoutState;
	if (!next.tabs.some((tab) => tab.type === "chat")) {
		const previousChat =
			state.tabs.find(
				(tab) =>
					tab.type === "chat" && tab.sessionId === state.currentSessionId,
			) ?? state.tabs.find((tab) => tab.type === "chat");
		// Generate the identity when dispatching, so replaying an action is pure.
		const fallbackId =
			action.field === "tabs" ? action.fallbackId : state.activeTabId;
		next.tabs = [
			draftChatTab(previousChat?.backendId ?? "wingman", fallbackId),
			...next.tabs,
		];
	}
	if (!next.tabs.some((tab) => paneOf(tab) === "left"))
		next.tabs = next.tabs.map((tab) => ({ ...tab, pane: undefined }));
	const active =
		next.tabs.find((tab) => tab.id === next.activeTabId) ?? next.tabs[0];
	next.activeTabId = active.id;
	const left = next.tabs.filter((tab) => paneOf(tab) === "left");
	const right = next.tabs.filter((tab) => paneOf(tab) === "right");
	next.leftActiveId =
		paneOf(active) === "left"
			? active.id
			: (left.find((tab) => tab.id === next.leftActiveId)?.id ??
				left[0]?.id ??
				"");
	next.rightActiveId =
		paneOf(active) === "right"
			? active.id
			: (right.find((tab) => tab.id === next.rightActiveId)?.id ??
				right[0]?.id ??
				"");
	if (active.type === "chat") next.currentSessionId = active.sessionId ?? "";
	else if (
		!next.tabs.some(
			(tab) => tab.type === "chat" && tab.sessionId === next.currentSessionId,
		)
	)
		next.currentSessionId =
			next.tabs.find((tab) => tab.type === "chat")?.sessionId ?? "";
	return next;
}
