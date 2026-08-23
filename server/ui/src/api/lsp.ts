import type {
	LSPActivityStatus,
	WorkspaceDiagnostics,
} from "../types/protocol";
import { fetchJSON, fetchOK } from "./http.ts";

export type LSPDocumentEvent = "open" | "change" | "save" | "close";

export function getWorkspaceDiagnostics(
	signal?: AbortSignal,
): Promise<WorkspaceDiagnostics> {
	return fetchJSON<WorkspaceDiagnostics>("/api/lsp/diagnostics", { signal });
}

export function getLSPActivity(
	signal?: AbortSignal,
): Promise<LSPActivityStatus> {
	return fetchJSON<LSPActivityStatus>("/api/lsp/status", { signal });
}

export async function syncLSPDocument(
	event: LSPDocumentEvent,
	path: string,
	content = "",
): Promise<void> {
	await fetchOK("/api/lsp/document", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ event, path, content }),
		keepalive: event === "close",
	});
}
