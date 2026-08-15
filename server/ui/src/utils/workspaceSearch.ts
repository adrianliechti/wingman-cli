import type { WorkspaceSearchFile, WorkspaceSearchMatch } from "../api/search";
import type { WorkspaceEditEnvelope } from "../workspaceEdit";

export function workspaceSearchMatchID(
	path: string,
	match: WorkspaceSearchMatch,
): string {
	return `${path}\u0000${match.line}:${match.column}:${match.end_column}`;
}

export function workspaceSearchEdit(
	files: WorkspaceSearchFile[],
): WorkspaceEditEnvelope {
	const changes: NonNullable<WorkspaceEditEnvelope["edit"]>["changes"] = {};
	const documents: WorkspaceEditEnvelope["documents"] = {};
	for (const file of files) {
		const edits = file.matches.map((match) => ({
			range: {
				start: { line: match.line - 1, character: match.column - 1 },
				end: { line: match.line - 1, character: match.end_column - 1 },
			},
			newText: match.replacement,
		}));
		if (edits.length === 0) continue;
		const uri = `wingman-search:${encodeURIComponent(file.path)}`;
		changes[uri] = edits;
		documents[uri] = {
			path: file.path,
			revision: file.revision,
			exists: true,
		};
	}
	return { edit: { changes }, documents };
}
