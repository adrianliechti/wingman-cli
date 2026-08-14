export type LSPDocumentEvent = "open" | "change" | "save" | "close";

export async function syncLSPDocument(
	event: LSPDocumentEvent,
	path: string,
	content = "",
): Promise<void> {
	const response = await fetch("/api/lsp/document", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ event, path, content }),
		keepalive: event === "close",
	});
	if (!response.ok) {
		throw new Error(
			(await response.text()).trim() || "LSP synchronization failed",
		);
	}
}
