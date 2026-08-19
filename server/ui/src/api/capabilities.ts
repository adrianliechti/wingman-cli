export interface Capabilities {
	git: boolean;
	git_init: boolean;
	lsp: boolean;
	debug: boolean;
	diffs: boolean;
	tasks: boolean;
	terminal: boolean;
	platform: string;
	workspace_name: string;
	notice?: string;
}

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
