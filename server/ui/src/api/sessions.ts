import { queryOptions } from "@tanstack/react-query";
import { fetchJSON } from "./http.ts";
import { queryKeys } from "./query.ts";
import { sessionKey } from "../state/sessionStore.ts";
import { workspaceClient } from "../state/workspaceClient.ts";
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
export async function deleteSession(key: string): Promise<void> {
	await workspaceClient().command(key, { type: "delete" });
}
export const sessionQueries = {
	list: (backend: string) =>
		queryOptions({
			queryKey: queryKeys.sessions.list(backend),
			queryFn: async ({ signal }) =>
				(
					await fetchJSON<SessionInfo[]>(
						`/api/v2/backends/${encodeURIComponent(backend)}/sessions`,
						{ signal },
					)
				).map((session) => ({
					...session,
					id: sessionKey(backend, session.id),
				})),
		}),
};
