const PRODUCT_NAMES: Readonly<Record<string, string>> = {
	claude: "Claude",
	codex: "Codex",
	copilot: "Copilot",
	opencode: "OpenCode",
	pi: "Pi",
};

export function formatAgentName(id: string, name?: string) {
	return PRODUCT_NAMES[id.trim().toLowerCase()] ?? name?.trim() ?? id;
}
