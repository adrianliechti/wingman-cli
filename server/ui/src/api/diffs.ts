import type { DiffEntry, DiffLayer } from "../types/protocol";
import { fetchJSON, fetchOK } from "./http.ts";

interface ListDiffsOptions {
	sessionId?: string;
	layer?: DiffLayer;
	path?: string;
	signal?: AbortSignal;
}

export function listDiffs({
	sessionId,
	layer,
	path,
	signal,
}: ListDiffsOptions = {}): Promise<DiffEntry[]> {
	const params = new URLSearchParams();
	if (path) params.set("path", path);
	if (layer) params.set("layer", layer);
	if (sessionId) params.set("session", sessionId);
	const query = params.toString();
	return fetchJSON<DiffEntry[]>(`/api/diffs${query ? `?${query}` : ""}`, {
		signal,
	});
}

export async function revertDiff(
	path: string,
	sessionId?: string,
): Promise<void> {
	const params = new URLSearchParams({ path });
	if (sessionId) params.set("session", sessionId);
	await fetchOK(`/api/diffs/revert?${params.toString()}`, { method: "POST" });
}
