import { useQuery } from "@tanstack/react-query";
import { capabilitiesQuery, type Capabilities } from "../api/capabilities";

export function useCapabilities(): Capabilities | null {
	return useQuery({
		...capabilitiesQuery,
		refetchInterval: (query) =>
			query.state.data?.managed_tools?.state === "installing" ? 500 : false,
	}).data ?? null;
}
