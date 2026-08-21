import { fetchJSON } from "./http.ts";

function localWebSocketURL(path: string): string {
	const protocol = location.protocol === "https:" ? "wss:" : "ws:";
	return `${protocol}//${location.host}${path}`;
}

export async function getChatWebSocketURL(): Promise<string> {
	try {
		const result = await fetchJSON<{ url?: string }>("/api/ws");
		if (result.url) return result.url;
	} catch {
		// The local endpoint remains usable when remote URL discovery is absent.
	}
	return localWebSocketURL("/ws");
}

export function getTerminalWebSocketURL(id: string): string {
	return localWebSocketURL(`/api/terminals/${encodeURIComponent(id)}/ws`);
}
