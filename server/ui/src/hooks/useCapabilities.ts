import { useQuery } from "@tanstack/react-query";
import { capabilitiesQuery, type Capabilities } from "../api/capabilities";

export function useCapabilities(): Capabilities | null {
	return useQuery(capabilitiesQuery).data ?? null;
}
