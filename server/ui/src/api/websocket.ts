import { workspaceURL } from "./http.ts";
function localWebSocketURL(path: string): string {
	const protocol = location.protocol === "https:" ? "wss:" : "ws:";
	return `${protocol}//${location.host}${path}`;
}

export function getTerminalWebSocketURL(id: string): string {
	return localWebSocketURL(
		workspaceURL(`/api/terminals/${encodeURIComponent(id)}/ws`),
	);
}
