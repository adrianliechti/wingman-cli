export interface WorkspaceSearchRequest {
	query: string;
	replacement: string;
	regex: boolean;
	case_sensitive: boolean;
	whole_word: boolean;
	include: string;
	exclude: string;
	limit?: number;
}

export interface WorkspaceSearchMatch {
	line: number;
	column: number;
	end_column: number;
	before: string;
	text: string;
	after: string;
	replacement: string;
}

export interface WorkspaceSearchFile {
	path: string;
	revision: string;
	matches: WorkspaceSearchMatch[];
}

export interface WorkspaceSearchSummary {
	files: number;
	matches: number;
	truncated: boolean;
}

interface WorkspaceSearchFileEvent {
	type: "file";
	file: WorkspaceSearchFile;
}

interface WorkspaceSearchDoneEvent extends WorkspaceSearchSummary {
	type: "done";
}

type WorkspaceSearchEvent = WorkspaceSearchFileEvent | WorkspaceSearchDoneEvent;

export async function streamWorkspaceSearch(
	request: WorkspaceSearchRequest,
	onFile: (file: WorkspaceSearchFile) => void,
	signal?: AbortSignal,
): Promise<WorkspaceSearchSummary> {
	const response = await fetch("/api/files/content-search", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(request),
		signal,
	});
	if (!response.ok) {
		const detail = (await response.text()).trim();
		throw new Error(detail || `Search failed (${response.status}).`);
	}
	if (!response.body) throw new Error("Search response was empty.");

	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let buffer = "";
	let summary: WorkspaceSearchSummary = {
		files: 0,
		matches: 0,
		truncated: false,
	};

	const accept = (line: string) => {
		if (!line.trim()) return;
		const event = JSON.parse(line) as WorkspaceSearchEvent;
		if (event.type === "file") {
			onFile(event.file);
			summary.files++;
			summary.matches += event.file.matches.length;
		} else if (event.type === "done") {
			summary = {
				files: event.files,
				matches: event.matches,
				truncated: event.truncated ?? false,
			};
		}
	};

	while (true) {
		const { value, done } = await reader.read();
		buffer += decoder.decode(value, { stream: !done });
		let newline = buffer.indexOf("\n");
		while (newline >= 0) {
			accept(buffer.slice(0, newline));
			buffer = buffer.slice(newline + 1);
			newline = buffer.indexOf("\n");
		}
		if (done) break;
	}
	accept(buffer);
	return summary;
}
