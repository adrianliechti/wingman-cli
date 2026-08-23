import { queryOptions } from "@tanstack/react-query";
import { fetchJSON, fetchOK } from "./http.ts";
import { queryKeys } from "./query.ts";

export interface SessionInfo {
	id: string;
	title?: string;
	updated_at: string;
}

export interface ModeOption {
	id: string;
	name: string;
	description?: string;
}

export interface ModeState {
	modes: ModeOption[];
	mode: string;
}

interface ModeResponse {
	modes?: ModeOption[];
	current?: string;
}

export async function createSession(): Promise<string> {
	const data = await fetchJSON<{ id?: string }>("/api/sessions", {
		method: "POST",
	});
	if (!data.id) throw new Error("Session id missing in response");
	return data.id;
}

export async function deleteSession(id: string): Promise<void> {
	await fetchOK(`/api/sessions/${encodeURIComponent(id)}`, {
		method: "DELETE",
	});
}

export async function loadSession(id: string): Promise<void> {
	await fetchOK(`/api/sessions/${encodeURIComponent(id)}/load`, {
		method: "POST",
	});
}

export async function setMode(
	sessionId: string,
	mode: string,
): Promise<ModeState> {
	const data = await fetchJSON<ModeResponse>(
		`/api/sessions/${encodeURIComponent(sessionId)}/mode`,
		{
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ mode }),
		},
	);
	return { modes: data.modes ?? [], mode: data.current ?? mode };
}

export const sessionQueries = {
	list: () =>
		queryOptions({
			queryKey: queryKeys.sessions.list,
			queryFn: ({ signal }) =>
				fetchJSON<SessionInfo[]>("/api/sessions", { signal }),
		}),
	mode: (sessionId?: string) =>
		queryOptions({
			queryKey: queryKeys.modes.current(sessionId),
			queryFn: async ({ signal }) => {
				const endpoint = sessionId
					? `/api/sessions/${encodeURIComponent(sessionId)}/mode`
					: "/api/mode";
				const data = await fetchJSON<ModeResponse>(endpoint, { signal });
				return { modes: data.modes ?? [], mode: data.current ?? "" };
			},
		}),
};
