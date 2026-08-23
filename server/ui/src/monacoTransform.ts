import type { Monaco } from "@monaco-editor/react";
import type * as MonacoTypes from "monaco-editor";
import type {
	EditorTransformEdit,
	EditorTransformRange,
	EditorTransformResponse,
} from "./api/editor";

interface TransformInlineCompletion
	extends MonacoTypes.languages.InlineCompletion {
	wingmanTransformID: number;
}

type TransformInlineCompletions =
	MonacoTypes.languages.InlineCompletions<TransformInlineCompletion>;

interface PendingTransform {
	id: number;
	version: number;
	edit: EditorTransformEdit;
}

export interface MonacoTransformBridge {
	preview(edit: EditorTransformEdit, version: number): boolean;
	dispose(): void;
}

export function isEditorTransformResponse(
	value: unknown,
): value is EditorTransformResponse {
	if (!value || typeof value !== "object") return false;
	const response = value as Record<string, unknown>;
	if (!Number.isInteger(response.version)) return false;
	if (response.edit === null) return true;
	if (!response.edit || typeof response.edit !== "object") return false;
	const edit = response.edit as Record<string, unknown>;
	return (
		typeof edit.expected_text === "string" &&
		typeof edit.replacement === "string" &&
		isEditorTransformRange(edit.range)
	);
}

function isEditorTransformRange(value: unknown): value is EditorTransformRange {
	if (!value || typeof value !== "object") return false;
	const range = value as Record<string, unknown>;
	const startLine = range.start_line;
	const startColumn = range.start_column;
	const endLine = range.end_line;
	const endColumn = range.end_column;
	if (
		!Number.isInteger(startLine) ||
		!Number.isInteger(startColumn) ||
		!Number.isInteger(endLine) ||
		!Number.isInteger(endColumn) ||
		(startLine as number) < 1 ||
		(startColumn as number) < 1 ||
		(endLine as number) < (startLine as number) ||
		(endColumn as number) < 1
	) {
		return false;
	}
	return (
		(endLine as number) > (startLine as number) ||
		(endColumn as number) > (startColumn as number)
	);
}

export function createMonacoTransformBridge(
	monaco: Monaco,
	editor: MonacoTypes.editor.IStandaloneCodeEditor,
): MonacoTransformBridge {
	const model = editor.getModel();
	if (!model) return { preview: () => false, dispose() {} };

	const changed = new monaco.Emitter<void>();
	let pending: PendingTransform | null = null;
	let sequence = 0;
	let disposed = false;
	const provider = monaco.languages.registerInlineCompletionsProvider(
		model.getLanguageId(),
		{
			groupId: "wingman.transform",
			displayName: "Wingman Transform",
			onDidChangeInlineCompletions: changed.event,
			provideInlineCompletions(
				candidateModel: MonacoTypes.editor.ITextModel,
			): TransformInlineCompletions {
				const current = pending;
				if (
					disposed ||
					!current ||
					candidateModel !== model ||
					model.getVersionId() !== current.version
				) {
					return emptyTransformCompletions();
				}
				const range = transformRange(monaco, model, current.edit);
				if (
					!range ||
					model.getValueInRange(range) !== current.edit.expected_text
				) {
					return emptyTransformCompletions();
				}
				return {
					items: [
						{
							insertText: current.edit.replacement,
							range,
							isInlineEdit: true,
							showInlineEditMenu: true,
							wingmanTransformID: current.id,
						},
					],
					suppressSuggestions: true,
				};
			},
			handleEndOfLifetime(
				_completions: TransformInlineCompletions,
				item: TransformInlineCompletion,
				reason: MonacoTypes.languages.InlineCompletionEndOfLifeReason<TransformInlineCompletion>,
			) {
				if (!pending || pending.id !== item.wingmanTransformID) return;
				if (
					reason.kind ===
						monaco.languages.InlineCompletionEndOfLifeReasonKind.Accepted ||
					reason.kind ===
						monaco.languages.InlineCompletionEndOfLifeReasonKind.Rejected
				) {
					pending = null;
				}
			},
			disposeInlineCompletions() {},
		},
	);

	return {
		preview(edit, version) {
			if (disposed || model.getVersionId() !== version) return false;
			const range = transformRange(monaco, model, edit);
			if (!range || model.getValueInRange(range) !== edit.expected_text) {
				return false;
			}
			pending = { id: ++sequence, version, edit };
			changed.fire();
			editor.setSelection(range);
			editor.focus();
			void editor.getAction("editor.action.inlineSuggest.trigger")?.run();
			return true;
		},
		dispose() {
			if (disposed) return;
			disposed = true;
			pending = null;
			provider.dispose();
			changed.dispose();
		},
	};
}

function emptyTransformCompletions(): TransformInlineCompletions {
	return { items: [], suppressSuggestions: false };
}

function transformRange(
	monaco: Monaco,
	model: MonacoTypes.editor.ITextModel,
	edit: EditorTransformEdit,
): MonacoTypes.Range | null {
	const range = edit.range;
	if (
		!Object.values(range).every(Number.isInteger) ||
		range.start_line < 1 ||
		range.start_column < 1 ||
		range.end_line < range.start_line ||
		(range.end_line === range.start_line &&
			range.end_column < range.start_column) ||
		range.end_column < 1
	) {
		return null;
	}
	try {
		const candidate = new monaco.Range(
			range.start_line,
			range.start_column,
			range.end_line,
			range.end_column,
		);
		const validated = model.validateRange(candidate);
		return validated.equalsRange(candidate) ? validated : null;
	} catch {
		return null;
	}
}
