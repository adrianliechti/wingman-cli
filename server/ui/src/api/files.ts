import type { FileContent } from "../types/protocol";

export interface CreateFileOptions {
	content?: string;
	directory?: boolean;
}

export interface WriteFileInput {
	path: string;
	content: string;
	revision?: string;
	force?: boolean;
}

export type WriteFileResult =
	| { ok: true; revision: string }
	| { ok: false; conflict: true; error: string };

export async function readWorkspaceFile(
	path: string,
	external = false,
): Promise<FileContent> {
	const endpoint = external ? "/api/lsp/file" : "/api/files/read";
	const response = await fetch(`${endpoint}?path=${encodeURIComponent(path)}`);
	if (!response.ok) throw await responseError(response, "Failed to load file");
	return response.json() as Promise<FileContent>;
}

export async function createWorkspaceFile(
	path: string,
	options: CreateFileOptions = {},
): Promise<FileContent | null> {
	const response = await fetch("/api/files", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ path, ...options }),
	});
	if (!response.ok) throw await responseError(response, "File creation failed");
	if (response.status === 204) return null;
	return response.json() as Promise<FileContent>;
}

export async function writeWorkspaceFile(
	input: WriteFileInput,
): Promise<WriteFileResult> {
	const response = await fetch("/api/files/write", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(input),
	});
	if (response.status === 409) {
		return {
			ok: false,
			conflict: true,
			error: (await response.text()).trim() || "File changed on disk.",
		};
	}
	if (!response.ok) throw await responseError(response, "Failed to save file");
	return { ok: true, ...((await response.json()) as { revision: string }) };
}

async function responseError(
	response: Response,
	fallback: string,
): Promise<Error> {
	const detail = (await response.text()).trim();
	return new Error(detail || `${fallback} (${response.status}).`);
}
