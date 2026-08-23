function localWebSocketURL(path: string): string {
	const protocol = location.protocol === "https:" ? "wss:" : "ws:";
	return `${protocol}//${location.host}${path}`;
}

export function getChatWebSocketURL(): string {
	return localWebSocketURL("/ws");
}

export function getTerminalWebSocketURL(id: string): string {
	return localWebSocketURL(`/api/terminals/${encodeURIComponent(id)}/ws`);
}
