import { queryOptions } from "@tanstack/react-query";
import type { FileContent, FileEntry } from "../types/protocol";
import { APIError, fetchJSON, fetchOK } from "./http.ts";
import { queryKeys } from "./query.ts";

export interface FileHit {
	path: string;
	name: string;
}

export function workspaceFilePreviewURL(path: string): string {
	return `/api/files/preview?path=${encodeURIComponent(path)}`;
}

export function workspaceFileDownloadURL(path: string): string {
	return `/api/files/download?path=${encodeURIComponent(path)}`;
}

export function isWorkspaceFileConflict(error: unknown): boolean {
	return error instanceof APIError && error.status === 409;
}

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

export interface BatchWriteFileInput {
	path: string;
	content: string;
	revision: string;
}

export type BatchWriteFileResult =
	| { ok: true; revisions: Record<string, string> }
	| { ok: false; conflict: true; error: string };

export async function deleteWorkspaceFile(path: string): Promise<void> {
	await fetchOK(`/api/files?path=${encodeURIComponent(path)}`, {
		method: "DELETE",
	});
}

export async function moveWorkspaceFile(
	from: string,
	to: string,
): Promise<void> {
	await fetchOK("/api/files/rename", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ from, to }),
	});
}

export async function copyWorkspaceFile(
	from: string,
	to: string,
): Promise<void> {
	await fetchOK("/api/files/copy", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ from, to }),
	});
}

export async function getAbsoluteWorkspacePath(path: string): Promise<string> {
	const result = await fetchJSON<{ path: string }>(
		`/api/files/path?path=${encodeURIComponent(path)}`,
	);
	return result.path;
}

export async function revealWorkspaceFile(path: string): Promise<void> {
	await fetchOK("/api/files/reveal", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ path }),
	});
}

export const fileQueries = {
	tree: (path: string) =>
		queryOptions({
			queryKey: queryKeys.files.tree(path),
			queryFn: ({ signal }) =>
				fetchJSON<FileEntry[]>(`/api/files?path=${encodeURIComponent(path)}`, {
					signal,
				}),
		}),
	search: (query: string) =>
		queryOptions({
			queryKey: queryKeys.files.search(query),
			queryFn: ({ signal }) =>
				fetchJSON<FileHit[]>(
					`/api/files/search?q=${encodeURIComponent(query)}`,
					{ signal },
				),
		}),
};

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

export async function writeWorkspaceFiles(
	files: BatchWriteFileInput[],
): Promise<BatchWriteFileResult> {
	const response = await fetch("/api/files/write-batch", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ files }),
	});
	if (response.status === 409) {
		return {
			ok: false,
			conflict: true,
			error: (await response.text()).trim() || "A file changed on disk.",
		};
	}
	if (!response.ok)
		throw await responseError(response, "Failed to apply workspace edit");
	return {
		ok: true,
		...((await response.json()) as { revisions: Record<string, string> }),
	};
}

async function responseError(
	response: Response,
	fallback: string,
): Promise<Error> {
	const detail = (await response.text()).trim();
	return new Error(detail || `${fallback} (${response.status}).`);
}
