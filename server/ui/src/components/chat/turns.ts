import type { ChatEntry } from "../../hooks/useWebSocket";

export interface Turn {
	key: string;
	user: ChatEntry | null;
	working: ChatEntry[];
	final: ChatEntry | null;
}

export function buildTurns(entries: ChatEntry[]): Turn[] {
	const turns: Turn[] = [];
	let counter = 0;

	for (const e of entries) {
		if (e.type === "user" || turns.length === 0) {
			turns.push({
				key: e.type === "user" ? e.id : `turn-${counter++}`,
				user: e.type === "user" ? e : null,
				working: [],
				final: null,
			});
			if (e.type === "user") continue;
		}
		const t = turns[turns.length - 1];
		if (t.final) {
			t.working.push(t.final);
			t.final = null;
		}
		if (e.type === "assistant") {
			t.final = e;
		} else {
			t.working.push(e);
		}
	}

	return turns;
}

export function findEntryElement(
	root: HTMLElement,
	id: string,
): HTMLElement | null {
	for (const el of root.querySelectorAll<HTMLElement>("[data-entry-id]")) {
		if (el.dataset.entryId === id) return el;
	}
	return null;
}
