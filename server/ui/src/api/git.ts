import type {
	CompareMode,
	GitBranches,
	GitCommit,
	GitCompare,
	GitStatus,
} from "../types/protocol";
import { fetchJSON, fetchOK } from "./http.ts";

export type GitRequestError = Error & { diagnosticContext: string };
export type GitAction =
	| "branches"
	| "checkout"
	| "commit"
	| "pull"
	| "push"
	| "stage"
	| "unstage";

export function getGitStatus(signal?: AbortSignal): Promise<GitStatus> {
	return fetchJSON<GitStatus>("/api/git/status", { signal });
}

export function getGitHistory(signal?: AbortSignal): Promise<GitCommit[]> {
	return fetchJSON<GitCommit[]>("/api/git/history", { signal });
}

export function getGitBranches(
	refresh: boolean,
	signal?: AbortSignal,
): Promise<GitBranches> {
	return fetchJSON<GitBranches>(
		`/api/git/branches?refresh=${refresh ? 1 : 0}`,
		{
			signal,
		},
	);
}

export async function runGitAction(
	action: GitAction,
	body?: unknown,
): Promise<string | undefined> {
	const response = await fetchOK(`/api/git/${action}`, {
		method: "POST",
		headers: body ? { "Content-Type": "application/json" } : undefined,
		body: body ? JSON.stringify(body) : undefined,
	});
	if (!response.headers.get("content-type")?.includes("application/json")) {
		return undefined;
	}
	return ((await response.json()) as { output?: string }).output;
}

export async function initializeGitRepository(): Promise<void> {
	await fetchOK("/api/git/init", { method: "POST" });
}

export async function generateGitCommitMessage(): Promise<string> {
	const result = await fetchJSON<unknown>("/api/git/commit-message", {
		method: "POST",
	});
	if (
		!result ||
		typeof result !== "object" ||
		!("message" in result) ||
		typeof result.message !== "string" ||
		!result.message.trim()
	) {
		throw new Error("The server returned an invalid commit message.");
	}
	return result.message;
}

export async function fetchGitComparison(
	base: string,
	head: string,
	mode: CompareMode,
	signal?: AbortSignal,
): Promise<GitCompare> {
	const params = new URLSearchParams({ base, head, mode });
	const endpoint = `/api/git/compare?${params.toString()}`;
	const requestContext = [
		"Operation: Compare Git revisions",
		`Request: GET ${endpoint}`,
		`Base: ${base}`,
		`Target: ${head}`,
		`Mode: ${mode}`,
	].join("\n");

	try {
		const response = await fetch(endpoint, { signal });
		if (!response.ok) throw await responseError(response, requestContext);
		return (await response.json()) as GitCompare;
	} catch (value) {
		const error = withDiagnosticContext(value, requestContext);
		console.error("Git comparison failed:", error, error.diagnosticContext);
		throw error;
	}
}

function withDiagnosticContext(
	value: unknown,
	context: string,
): GitRequestError {
	const error = (
		value instanceof Error ? value : new Error(String(value))
	) as GitRequestError;
	error.diagnosticContext ||= context;
	return error;
}

async function responseError(
	response: Response,
	requestContext: string,
): Promise<GitRequestError> {
	const serverResponse = (await response.text()).trim();
	const status = `${response.status}${response.statusText ? ` ${response.statusText}` : ""}`;
	const error = new Error(serverResponse || status) as GitRequestError;
	error.name = "GitCompareRequestError";
	error.diagnosticContext = [
		requestContext,
		`Response: HTTP ${status}`,
		serverResponse ? `Server response:\n${serverResponse}` : "",
	]
		.filter(Boolean)
		.join("\n\n");
	return error;
}
