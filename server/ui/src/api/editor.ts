import { fetchJSON } from "./http.ts";

export interface TabPredictionRequest {
	path: string;
	content: string;
	previous_content: string;
	line: number;
	column: number;
	version: number;
}

export function getTabPrediction(
	request: TabPredictionRequest,
	signal?: AbortSignal,
): Promise<unknown> {
	return fetchJSON<unknown>("/api/editor/tab", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(request),
		signal,
	});
}

export interface EditorTransformRange {
	start_line: number;
	start_column: number;
	end_line: number;
	end_column: number;
}

export interface EditorTransformRequest {
	path: string;
	content: string;
	range: EditorTransformRange;
	instruction: string;
	version: number;
}

export interface EditorTransformEdit {
	range: EditorTransformRange;
	expected_text: string;
	replacement: string;
}

export interface EditorTransformResponse {
	edit: EditorTransformEdit | null;
	version: number;
}

export function transformEditorSelection(
	request: EditorTransformRequest,
	signal?: AbortSignal,
): Promise<unknown> {
	return fetchJSON<unknown>("/api/editor/transform", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(request),
		signal,
	});
}
