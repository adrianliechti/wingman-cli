import type { TextEdit, WorkspaceEdit } from "vscode-languageserver-types";

export interface WorkspaceDocumentSnapshot {
	path: string;
	revision?: string;
	exists: boolean;
}

export async function contentRevision(content: string): Promise<string> {
	const digest = await crypto.subtle.digest(
		"SHA-256",
		new TextEncoder().encode(content),
	);
	return Array.from(new Uint8Array(digest), (value) =>
		value.toString(16).padStart(2, "0"),
	).join("");
}

export interface WorkspaceEditEnvelope {
	edit: WorkspaceEdit | null;
	documents: Record<string, WorkspaceDocumentSnapshot>;
}

export interface WorkspaceEditSummary {
	files: string[];
	edits: number;
	requiresConfirmation: boolean;
}

export function summarizeWorkspaceEdit(
	envelope: WorkspaceEditEnvelope,
): WorkspaceEditSummary {
	const operations = textEditOperations(envelope);
	let edits = 0;
	for (const groups of operations.values()) {
		for (const group of groups) edits += group.edits.length;
	}
	const annotations = envelope.edit?.changeAnnotations ?? {};
	return {
		files: Array.from(operations.values(), (groups) => groups[0].path).sort(),
		edits,
		requiresConfirmation:
			operations.size > 1 ||
			Object.values(annotations).some(
				(annotation) => annotation.needsConfirmation === true,
			),
	};
}

export function textEditOperations(
	envelope: WorkspaceEditEnvelope,
): Map<string, Array<{ path: string; edits: TextEdit[] }>> {
	const result = new Map<string, Array<{ path: string; edits: TextEdit[] }>>();
	const edit = envelope.edit;
	if (!edit) return result;

	const add = (documentUri: string, edits: unknown[]) => {
		const document = envelope.documents[documentUri];
		if (!document)
			throw new Error("The language server returned an unknown document URI.");
		if (!document.exists || !document.revision)
			throw new Error(
				`Workspace edit cannot modify missing file “${document.path}”.`,
			);
		const textEdits = edits.map((item) => {
			if (!isTextEdit(item)) {
				throw new Error(
					"The language server returned a snippet edit that this editor did not advertise.",
				);
			}
			return item;
		});
		const groups = result.get(documentUri) ?? [];
		groups.push({ path: document.path, edits: textEdits });
		result.set(documentUri, groups);
	};

	// The LSP says documentChanges take precedence when both representations
	// are present. Keeping each group separate also preserves their ordering.
	if (edit.documentChanges) {
		for (const change of edit.documentChanges) {
			if ("kind" in change) {
				throw new Error(
					"The language server returned a file operation that was not advertised as supported.",
				);
			}
			add(change.textDocument.uri, change.edits);
		}
		return result;
	}

	for (const [documentUri, edits] of Object.entries(edit.changes ?? {})) {
		add(documentUri, edits);
	}
	return result;
}

export function applyTextEdits(content: string, edits: TextEdit[]): string {
	const positioned = edits.map((edit, index) => ({
		edit,
		index,
		start: offsetAt(content, edit.range.start.line, edit.range.start.character),
		end: offsetAt(content, edit.range.end.line, edit.range.end.character),
	}));
	for (const item of positioned) {
		if (item.end < item.start)
			throw new Error("Invalid reversed text edit range.");
	}

	const ascending = [...positioned].sort(
		(a, b) => a.start - b.start || a.end - b.end || a.index - b.index,
	);
	for (let index = 1; index < ascending.length; index++) {
		if (ascending[index].start < ascending[index - 1].end) {
			throw new Error("The language server returned overlapping text edits.");
		}
	}

	positioned.sort(
		(a, b) => b.start - a.start || b.end - a.end || b.index - a.index,
	);
	let result = content;
	for (const item of positioned) {
		result =
			result.slice(0, item.start) + item.edit.newText + result.slice(item.end);
	}
	return result;
}

function isTextEdit(value: unknown): value is TextEdit {
	return (
		typeof value === "object" &&
		value !== null &&
		"range" in value &&
		"newText" in value &&
		typeof value.newText === "string"
	);
}

function offsetAt(content: string, line: number, character: number): number {
	if (
		!Number.isInteger(line) ||
		!Number.isInteger(character) ||
		line < 0 ||
		character < 0
	) {
		throw new Error("Invalid text edit position.");
	}
	let currentLine = 0;
	let lineStart = 0;
	for (
		let offset = 0;
		offset < content.length && currentLine < line;
		offset++
	) {
		if (content.charCodeAt(offset) === 10) {
			currentLine++;
			lineStart = offset + 1;
		}
	}
	if (currentLine !== line)
		throw new Error("Text edit line is outside the document.");
	let lineEnd = content.indexOf("\n", lineStart);
	if (lineEnd < 0) lineEnd = content.length;
	if (lineEnd > lineStart && content.charCodeAt(lineEnd - 1) === 13) lineEnd--;
	if (character > lineEnd - lineStart) {
		throw new Error("Text edit character is outside the line.");
	}
	return lineStart + character;
}
