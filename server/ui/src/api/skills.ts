import { queryOptions } from "@tanstack/react-query";
import { fetchJSON } from "./http.ts";
import { queryKeys } from "./query.ts";

export interface Skill {
	name: string;
	description?: string;
	input_hint?: string;
}

export function skillsQuery(sessionId?: string) {
	return queryOptions({
		queryKey: queryKeys.skills.list(sessionId),
		// Listing skills is also the server-side refresh trigger. Reopening the
		// picker must perform a request even if the last catalog is cached.
		staleTime: 0,
		queryFn: ({ signal }) => {
			const endpoint = sessionId
				? `/api/skills?session=${encodeURIComponent(sessionId)}`
				: "/api/skills";
			return fetchJSON<Skill[]>(endpoint, { signal });
		},
	});
}
