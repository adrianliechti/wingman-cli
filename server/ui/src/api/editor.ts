import { fetchJSON } from "./http.ts";

export interface TabPredictionRequest {
	path: string;
	content: string;
	previous_content: string;
	line: number;
	column: number;
	version: number;
}

export function getTabPrediction(
	request: TabPredictionRequest,
	signal?: AbortSignal,
): Promise<unknown> {
	return fetchJSON<unknown>("/api/editor/tab", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(request),
		signal,
	});
}
