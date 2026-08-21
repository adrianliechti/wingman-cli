import { fetchJSON, fetchOK } from "./http.ts";

export async function chooseWorkspaceFolder(): Promise<string | undefined> {
	const result = await fetchJSON<{ path?: string }>("/app/folder", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: "{}",
	});
	return result.path;
}

export async function replaceWorkspace(path: string): Promise<void> {
	await fetchOK("/app/workspaces/open", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ path, replace: true }),
	});
}
