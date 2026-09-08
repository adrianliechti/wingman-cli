import { queryOptions } from "@tanstack/react-query";
import { fetchJSON } from "./http.ts";
import { queryKeys } from "./query.ts";

export interface Capabilities {
	git: boolean;
	git_init: boolean;
	lsp: boolean;
	debug: boolean;
	tasks: boolean;
	terminal: boolean;
	tab: boolean;
	"editor.tab.completion": boolean;
	"window.terminal.position": "tab" | "bottom";
	"window.sidebar.position": "left" | "right";
	platform: string;
	workspace_name: string;
	managed_tools?: ManagedToolsStatus;
}

export interface ManagedToolsStatus {
	state: "installing" | "ready" | "error";
	tool?: string;
	label?: string;
	phase?: "checking" | "installing" | "updating";
	current?: number;
	total?: number;
	error?: string;
	unavailable?: string[];
}

export const capabilitiesQuery = queryOptions({
	queryKey: queryKeys.capabilities,
	queryFn: ({ signal }) =>
		fetchJSON<Capabilities>("/api/capabilities", { signal }),
});

export interface InspectAvailability {
	inspect: boolean;
	diagnostics: boolean;
	debug: boolean;
}

export function getInspectAvailability(
	capabilities: Pick<Capabilities, "lsp" | "debug"> | null,
): InspectAvailability {
	const diagnostics = capabilities?.lsp ?? false;
	const debug = capabilities?.debug ?? false;
	return { inspect: diagnostics, diagnostics, debug };
}
