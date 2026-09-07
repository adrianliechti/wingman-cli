import { sessionKey } from "../state/sessionStore.ts";
import { QueryClient, type QueryKey } from "@tanstack/react-query";
import type { ServerMessage } from "../types/protocol";

export const queryKeys = {
	all: ["server"] as const,
	capabilities: ["server", "capabilities"] as const,
	sessions: {
		all: ["server", "sessions"] as const,
		list: (backend: string) => ["server", "sessions", "list", backend] as const,
	},
	skills: {
		all: ["server", "skills"] as const,
		list: (sessionId?: string) =>
			["server", "skills", sessionId ?? ""] as const,
	},
	terminals: {
		all: ["server", "terminals"] as const,
		list: ["server", "terminals", "list"] as const,
		shells: ["server", "terminals", "shells"] as const,
	},
	diagnostics: {
		all: ["server", "diagnostics"] as const,
		workspace: ["server", "diagnostics", "workspace"] as const,
	},
	lsp: {
		all: ["server", "lsp"] as const,
		activity: ["server", "lsp", "activity"] as const,
	},
	debug: {
		all: ["server", "debug"] as const,
		session: ["server", "debug", "session"] as const,
		state: ["server", "debug", "state"] as const,
		inspection: ["server", "debug", "inspection"] as const,
		output: ["server", "debug", "output"] as const,
		scopes: (sessionId: string, stateVersion: number, frameId: number) =>
			["server", "debug", "scopes", sessionId, stateVersion, frameId] as const,
		variables: (sessionId: string, stateVersion: number, reference: number) =>
			[
				"server",
				"debug",
				"variables",
				sessionId,
				stateVersion,
				reference,
			] as const,
	},
	insights: {
		all: ["server", "insights"] as const,
		overview: ["server", "insights", "overview"] as const,
		modules: ["server", "insights", "modules"] as const,
		activity: ["server", "insights", "activity"] as const,
		summaries: (modules: readonly string[], cachedOnly: boolean) =>
			["server", "insights", "summaries", cachedOnly, ...modules] as const,
		symbolSearch: (options: object) =>
			["server", "insights", "symbol-search", options] as const,
		contentSearch: (options: object) =>
			["server", "insights", "content-search", options] as const,
		symbol: (focus: object) => ["server", "insights", "symbol", focus] as const,
	},
	diffs: {
		all: ["server", "diffs"] as const,
		list: (layer?: string, path?: string) =>
			["server", "diffs", "list", layer ?? "", path ?? ""] as const,
	},
	git: {
		all: ["server", "git"] as const,
		status: ["server", "git", "status"] as const,
		history: ["server", "git", "history"] as const,
		branches: ["server", "git", "branches"] as const,
		compare: (base: string, head: string, mode: string) =>
			["server", "git", "compare", base, head, mode] as const,
		comparisons: ["server", "git", "compare"] as const,
	},
	files: {
		all: ["server", "files"] as const,
		tree: (path: string) => ["server", "files", "tree", path] as const,
		search: (query: string) => ["server", "files", "search", query] as const,
	},
	tasks: {
		all: ["server", "tasks"] as const,
		session: (sessionId: string) => ["server", "tasks", sessionId] as const,
		list: (sessionId: string) =>
			["server", "tasks", sessionId, "list"] as const,
		schedules: (sessionId: string) =>
			["server", "tasks", sessionId, "schedules"] as const,
		detail: (sessionId: string, taskId: string) =>
			["server", "tasks", sessionId, "detail", taskId] as const,
	},
} satisfies Record<string, QueryKey | Record<string, unknown>>;

export const createServerQueryClient = () =>
	new QueryClient({
		defaultOptions: {
			queries: {
				// The WebSocket bridge invalidates normal server changes. Infinity keeps
				// fresh data quiet while TanStack's focus/reconnect defaults can still
				// recover queries that were explicitly invalidated or use staleTime: 0.
				staleTime: Infinity,
				// Expected HTTP errors should surface immediately. Volatile reads use
				// polling, focus, reconnect, or WebSocket invalidation for recovery.
				retry: false,
			},
		},
	});

export function invalidateAllServerQueries(client: QueryClient): void {
	void client.invalidateQueries({ queryKey: queryKeys.all });
}

export async function invalidateGitIndexQueries(
	client: QueryClient,
): Promise<void> {
	// Status drives the Changes panel. Refresh it first, and reuse an in-flight
	// request when both the mutation and its WebSocket event reach this client.
	await client.invalidateQueries(
		{ queryKey: queryKeys.git.status },
		{ cancelRefetch: false },
	);
	void client.invalidateQueries(
		{ queryKey: queryKeys.diffs.all },
		{ cancelRefetch: false },
	);
	void client.invalidateQueries(
		{
			queryKey: queryKeys.git.comparisons,
			predicate: ({ queryKey }) => queryKey.includes(":worktree"),
		},
		{ cancelRefetch: false },
	);
}

export function invalidateForServerMessage(
	client: QueryClient,
	message: ServerMessage,
): void {
	const invalidate = (queryKey: QueryKey) => {
		void client.invalidateQueries({ queryKey });
	};

	switch (message.type) {
		case "capabilities_changed":
			invalidate(queryKeys.capabilities);
			break;
		case "model_changed":
			invalidate(["server", "backend-settings"]);
			break;
		case "sessions_changed":
			invalidate(queryKeys.sessions.all);
			break;
		case "skills_changed":
			invalidate(queryKeys.skills.all);
			break;
		case "terminals_changed":
			invalidate(queryKeys.terminals.all);
			break;
		case "diagnostics_changed":
			invalidate(queryKeys.diagnostics.all);
			break;
		case "diffs_changed":
			invalidate(queryKeys.diffs.all);
			invalidate(queryKeys.git.all);
			break;
		case "git_index_changed":
			void invalidateGitIndexQueries(client);
			break;
		case "files_changed":
			invalidate(queryKeys.files.all);
			// The indexed graph is unchanged until reindexing, but its coverage
			// status must report that the source tree is now stale.
			invalidate(queryKeys.insights.overview);
			break;
		case "tasks_changed":
			invalidate(
				message.session
					? queryKeys.tasks.session(
							sessionKey(message.backend ?? "wingman", message.session),
						)
					: queryKeys.tasks.all,
			);
			break;
	}
}
