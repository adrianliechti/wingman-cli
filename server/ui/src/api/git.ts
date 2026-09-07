import { scopedFetch } from "./http.ts";
import type {
	CompareMode,
	GitBranches,
	GitCommit,
	GitCompare,
	GitStatus,
} from "../types/protocol";
import { fetchJSON, fetchOK } from "./http.ts";

export type GitRequestError = Error & { diagnosticContext: string };
export type GitCommand =
	| { type: "create_branch"; name: string }
	| { type: "checkout_branch"; name: string; remote?: string }
	| { type: "commit"; message: string }
	| { type: "pull" }
	| { type: "push" }
	| { type: "stage"; paths: string[] }
	| { type: "stage_all" }
	| { type: "unstage"; paths: string[] };
export type GitCommandType = GitCommand["type"];

export function getGitStatus(signal?: AbortSignal): Promise<GitStatus> {
	return fetchJSON<GitStatus>("/api/git/status", { signal });
}

export function getGitHistory(signal?: AbortSignal): Promise<GitCommit[]> {
	return fetchJSON<GitCommit[]>("/api/git/history", { signal });
}

export function getGitBranches(signal?: AbortSignal): Promise<GitBranches> {
	return fetchJSON<GitBranches>("/api/git/branches", { signal });
}

export function fetchGitBranches(): Promise<GitBranches> {
	return fetchJSON<GitBranches>("/api/git/fetch", { method: "POST" });
}

export async function runGitCommand(
	command: GitCommand,
): Promise<string | undefined> {
	let endpoint: string;
	let body: object | undefined;
	switch (command.type) {
		case "create_branch":
			endpoint = "branches";
			body = { name: command.name };
			break;
		case "checkout_branch":
			endpoint = "checkout";
			body = {
				name: command.name,
				...(command.remote ? { remote: command.remote } : {}),
			};
			break;
		case "commit":
			endpoint = "commit";
			body = { message: command.message };
			break;
		case "pull":
		case "push":
			endpoint = command.type;
			break;
		case "stage":
		case "unstage":
			endpoint = command.type;
			body = { paths: command.paths };
			break;
		case "stage_all":
			endpoint = "stage";
			break;
	}
	const response = await fetchOK(`/api/git/${endpoint}`, {
		method: "POST",
		headers: body ? { "Content-Type": "application/json" } : undefined,
		body: body ? JSON.stringify(body) : undefined,
	});
	if (response.status === 204) return undefined;
	const result = (await response.json()) as { output?: unknown };
	if (typeof result.output !== "string") {
		throw new Error("The server returned an invalid Git mutation response.");
	}
	return result.output;
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
		const response = await scopedFetch(endpoint, { signal });
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
