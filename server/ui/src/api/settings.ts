import { fetchOK } from "./http.ts";

export async function setEditorTabCompletion(enabled: boolean): Promise<void> {
	await fetchOK("/api/settings/editor.tab.completion", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ "editor.tab.completion": enabled }),
	});
}

export async function setWindowTerminalPosition(
	position: "tab" | "bottom",
): Promise<void> {
	await fetchOK("/api/settings/window.terminal.position", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ "window.terminal.position": position }),
	});
}
