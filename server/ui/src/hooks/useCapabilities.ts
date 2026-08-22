import { useQuery } from "@tanstack/react-query";
import { capabilitiesQuery, type Capabilities } from "../api/capabilities";

export function useCapabilities(): Capabilities | null {
	return (
		useQuery({
			...capabilitiesQuery,
			// The capabilities-changed broadcast is the primary signal; polling only
			// backstops a dropped socket while installs are running.
			refetchInterval: (query) =>
				query.state.data?.managed_tools?.state === "installing" ? 2000 : false,
		}).data ?? null
	);
}
