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
		| "graph"
		| "browser";
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

export function draftChatTab(): CenterTab {
	return {
		id: chatTabId(""),
		type: "chat",
		label: "Agent",
		sessionId: "",
	};
}

export function withSessionFallback(tabs: CenterTab[]): CenterTab[] {
	return tabs.some((tab) => tab.type === "chat")
		? tabs
		: [draftChatTab(), ...tabs];
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
