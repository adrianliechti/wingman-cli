import { queryOptions } from "@tanstack/react-query";
import type { ShellEntry, TerminalEntry } from "../types/protocol";
import { APIError, fetchJSON, fetchOK } from "./http.ts";
import { queryKeys } from "./query.ts";

export const terminalQueries = {
	shells: () =>
		queryOptions({
			queryKey: queryKeys.terminals.shells,
			queryFn: ({ signal }) =>
				fetchJSON<ShellEntry[]>("/api/terminals/shells", { signal }),
		}),
	list: () =>
		queryOptions({
			queryKey: queryKeys.terminals.list,
			queryFn: ({ signal }) =>
				fetchJSON<TerminalEntry[]>("/api/terminals", { signal }),
		}),
};

export function startTerminal(shell?: string): Promise<TerminalEntry> {
	return fetchJSON<TerminalEntry>("/api/terminals", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ shell, cols: 80, rows: 24 }),
	});
}

export async function getTerminal(id: string): Promise<TerminalEntry | null> {
	try {
		return await fetchJSON<TerminalEntry>(
			`/api/terminals/${encodeURIComponent(id)}`,
		);
	} catch (error) {
		if (error instanceof APIError && error.status === 404) return null;
		throw error;
	}
}

export async function deleteTerminal(id: string): Promise<void> {
	try {
		await fetchOK(`/api/terminals/${encodeURIComponent(id)}`, {
			method: "DELETE",
		});
	} catch (error) {
		if (!(error instanceof APIError && error.status === 404)) throw error;
	}
}
