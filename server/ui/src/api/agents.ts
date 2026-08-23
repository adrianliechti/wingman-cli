import { queryOptions } from "@tanstack/react-query";
import { fetchJSON, fetchOK } from "./http.ts";
import { queryKeys } from "./query.ts";

export interface AgentInfo {
	id: string;
	name: string;
}

export interface AgentState {
	agent: string;
	can_delete: boolean;
}

export async function setCurrentAgent(agent: string): Promise<void> {
	await fetchOK("/api/agent", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ agent }),
	});
}

export const agentQueries = {
	list: () =>
		queryOptions({
			queryKey: queryKeys.agents.list,
			queryFn: ({ signal }) =>
				fetchJSON<AgentInfo[]>("/api/agents", { signal }),
		}),
	current: () =>
		queryOptions({
			queryKey: queryKeys.agents.current,
			queryFn: ({ signal }) => fetchJSON<AgentState>("/api/agent", { signal }),
		}),
};
