import { queryOptions } from "@tanstack/react-query";
import { fetchJSON } from "./http.ts";
import { queryKeys } from "./query.ts";

export interface ModelInfo {
	id: string;
	name: string;
	namespace?: string;
}

export interface ModelState {
	model?: string;
}

export interface EffortState {
	effort?: string;
	options?: string[];
}

function sessionAPIBase(sessionId?: string): string {
	return sessionId ? `/api/sessions/${encodeURIComponent(sessionId)}` : "/api";
}

export function setCurrentModel(
	model: string,
	sessionId?: string,
): Promise<ModelState> {
	return fetchJSON<ModelState>(`${sessionAPIBase(sessionId)}/model`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ model }),
	});
}

export function setCurrentEffort(
	effort: string,
	sessionId?: string,
): Promise<EffortState> {
	return fetchJSON<EffortState>(`${sessionAPIBase(sessionId)}/effort`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ effort }),
	});
}

export const modelQueries = {
	list: () =>
		queryOptions({
			queryKey: queryKeys.models.list,
			queryFn: ({ signal }) =>
				fetchJSON<ModelInfo[]>("/api/models", { signal }),
		}),
	current: (sessionId?: string) =>
		queryOptions({
			queryKey: queryKeys.models.current(sessionId),
			queryFn: ({ signal }) =>
				fetchJSON<ModelState>(`${sessionAPIBase(sessionId)}/model`, {
					signal,
				}),
		}),
	effort: (sessionId?: string) =>
		queryOptions({
			queryKey: queryKeys.models.effort(sessionId),
			queryFn: ({ signal }) =>
				fetchJSON<EffortState>(`${sessionAPIBase(sessionId)}/effort`, {
					signal,
				}),
		}),
};
