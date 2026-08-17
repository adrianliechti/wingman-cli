import type { Monaco } from "@monaco-editor/react";
import type * as MonacoTypes from "monaco-editor";

const MAX_DOCUMENT_LENGTH = 1 << 20;
const BURST_GAP_MS = 1_000;
// Ordinary typing and cursor movement cancel stale work much sooner. This is
// only a transport safety valve for a request whose editor state stayed valid.
const REQUEST_TIMEOUT_MS = 6_000;
const MIN_DEBOUNCE_MS = 100;
const MAX_DEBOUNCE_MS = 700;

interface TabPredictionRange {
	start_line: number;
	start_column: number;
	end_line: number;
	end_column: number;
}

export interface TabPredictionEdit {
	insert_text: string;
	expected_text: string;
	range: TabPredictionRange;
}

interface TabPredictionResponse {
	edit: TabPredictionEdit | null;
	version: number;
}

interface TabInlineCompletion extends MonacoTypes.languages.InlineCompletion {
	wingmanTab: true;
}

type TabInlineCompletions =
	MonacoTypes.languages.InlineCompletions<TabInlineCompletion>;

interface MonacoTabOptions {
	monaco: Monaco;
	editor: MonacoTypes.editor.IStandaloneCodeEditor;
	path: string;
	onAccepted?: () => void | Promise<void>;
}

export interface MonacoTabBridge {
	dispose(): void;
}

export function createMonacoTabBridge({
	monaco,
	editor,
	path,
	onAccepted,
}: MonacoTabOptions): MonacoTabBridge {
	const model = editor.getModel();
	if (!model) return { dispose() {} };

	const disposables: MonacoTypes.IDisposable[] = [];
	const cache = new Map<string, TabPredictionEdit | null>();
	let disposed = false;
	let engaged = false;
	let previousValue = model.getValue();
	let burstBefore = previousValue;
	let lastChangeAt = 0;
	let debounceMs = 220;
	let rejectionUntil = 0;
	let activeController: AbortController | null = null;
	let cursorTriggerTimer: number | null = null;
	let postAcceptScheduled = false;

	function abortActiveRequest() {
		activeController?.abort();
		activeController = null;
	}

	function handleAccepted() {
		if (disposed || postAcceptScheduled) return;
		postAcceptScheduled = true;
		debounceMs = Math.max(MIN_DEBOUNCE_MS, debounceMs - 30);
		rejectionUntil = 0;
		window.setTimeout(() => {
			postAcceptScheduled = false;
			if (!disposed) void onAccepted?.();
		}, 0);
	}

	disposables.push(
		model.onDidChangeContent((event) => {
			const current = model.getValue();
			abortActiveRequest();
			if (event.isFlush) {
				engaged = false;
				burstBefore = current;
			} else {
				const now = Date.now();
				if (now - lastChangeAt > BURST_GAP_MS) burstBefore = previousValue;
				lastChangeAt = now;
				engaged = true;
			}
			previousValue = current;
		}),
		editor.onDidChangeCursorPosition(() => {
			abortActiveRequest();
			if (disposed || !engaged) return;
			if (cursorTriggerTimer !== null) window.clearTimeout(cursorTriggerTimer);
			cursorTriggerTimer = window.setTimeout(() => {
				cursorTriggerTimer = null;
				if (!disposed)
					void editor.getAction("editor.action.inlineSuggest.trigger")?.run();
			}, 0);
		}),
	);

	disposables.push(
		monaco.languages.registerInlineCompletionsProvider(model.getLanguageId(), {
			groupId: "wingman.tab",
			displayName: "Wingman Tab",
			debounceDelayMs: 0,
			async provideInlineCompletions(
				candidateModel: MonacoTypes.editor.ITextModel,
				position: MonacoTypes.Position,
				context: MonacoTypes.languages.InlineCompletionContext,
				token: MonacoTypes.CancellationToken,
			): Promise<TabInlineCompletions> {
				const empty = emptyTabCompletions();
				if (
					disposed ||
					candidateModel !== model ||
					!engaged ||
					candidateModel.getValueLength() > MAX_DOCUMENT_LENGTH ||
					context.selectedSuggestionInfo ||
					Date.now() < rejectionUntil
				) {
					return empty;
				}
				const selection = editor.getSelection();
				if (!selection?.isEmpty()) return empty;

				const content = candidateModel.getValue();
				const version = candidateModel.getVersionId();
				const key = tabCacheKey(
					path,
					content,
					burstBefore,
					position.lineNumber,
					position.column,
				);
				if (!cache.has(key) && !(await waitForTabDebounce(debounceMs, token))) {
					return empty;
				}

				let edit = cache.get(key);
				if (edit === undefined) {
					const controller = new AbortController();
					abortActiveRequest();
					activeController = controller;
					const cancellation = token.onCancellationRequested(() =>
						controller.abort(),
					);
					const timeout = window.setTimeout(
						() => controller.abort(),
						REQUEST_TIMEOUT_MS,
					);
					try {
						const response = await fetch("/api/editor/tab", {
							method: "POST",
							headers: { "Content-Type": "application/json" },
							body: JSON.stringify({
								path,
								content,
								previous_content: burstBefore,
								line: position.lineNumber,
								column: position.column,
								version,
							}),
							signal: controller.signal,
						});
						if (!response.ok) return empty;
						const result: unknown = await response.json();
						if (
							!isTabPredictionResponse(result) ||
							result.version !== version
						) {
							return empty;
						}
						edit = result.edit;
						rememberTabPrediction(cache, key, edit);
					} catch {
						return empty;
					} finally {
						window.clearTimeout(timeout);
						cancellation.dispose();
						if (activeController === controller) activeController = null;
					}
				}

				if (
					!edit ||
					token.isCancellationRequested ||
					candidateModel.getVersionId() !== version ||
					candidateModel.getValue() !== content
				) {
					return empty;
				}
				const range = predictionRange(monaco, candidateModel, edit);
				if (
					!range ||
					candidateModel.getValueInRange(range) !== edit.expected_text
				) {
					return empty;
				}
				const insertionAtCursor =
					edit.expected_text === "" &&
					range.isEmpty() &&
					range.getStartPosition().equals(position);
				const isInlineEdit = !insertionAtCursor;
				return {
					items: [
						{
							insertText: edit.insert_text,
							range,
							isInlineEdit,
							showInlineEditMenu: isInlineEdit,
							wingmanTab: true,
						},
					],
					suppressSuggestions: false,
					enableForwardStability: true,
				};
			},
			handleEndOfLifetime(
				_completions: TabInlineCompletions,
				_item: TabInlineCompletion,
				reason: MonacoTypes.languages.InlineCompletionEndOfLifeReason<TabInlineCompletion>,
				_summary: MonacoTypes.languages.LifetimeSummary,
			) {
				if (
					reason.kind ===
					monaco.languages.InlineCompletionEndOfLifeReasonKind.Accepted
				) {
					handleAccepted();
					return;
				}
				if (
					reason.kind ===
						monaco.languages.InlineCompletionEndOfLifeReasonKind.Rejected ||
					(reason.kind ===
						monaco.languages.InlineCompletionEndOfLifeReasonKind.Ignored &&
						"userTypingDisagreed" in reason &&
						reason.userTypingDisagreed)
				) {
					debounceMs = Math.min(MAX_DEBOUNCE_MS, debounceMs + 90);
					rejectionUntil = Date.now() + 750;
				}
			},
			disposeInlineCompletions() {},
		}),
	);

	return {
		dispose() {
			if (disposed) return;
			disposed = true;
			if (cursorTriggerTimer !== null) window.clearTimeout(cursorTriggerTimer);
			abortActiveRequest();
			for (const disposable of disposables) disposable.dispose();
			cache.clear();
		},
	};
}

function emptyTabCompletions(): TabInlineCompletions {
	return { items: [], suppressSuggestions: false };
}

function predictionRange(
	monaco: Monaco,
	model: MonacoTypes.editor.ITextModel,
	edit: TabPredictionEdit,
): MonacoTypes.Range | null {
	const { range } = edit;
	if (
		![
			range.start_line,
			range.start_column,
			range.end_line,
			range.end_column,
		].every(Number.isInteger) ||
		range.start_line < 1 ||
		range.start_column < 1 ||
		range.end_line < range.start_line ||
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

export function isTabPredictionResponse(
	value: unknown,
): value is TabPredictionResponse {
	if (!value || typeof value !== "object") return false;
	const response = value as Record<string, unknown>;
	if (!Number.isInteger(response.version)) return false;
	if (response.edit === null) return true;
	if (!response.edit || typeof response.edit !== "object") return false;
	const edit = response.edit as Record<string, unknown>;
	if (
		typeof edit.insert_text !== "string" ||
		typeof edit.expected_text !== "string" ||
		!edit.range ||
		typeof edit.range !== "object"
	) {
		return false;
	}
	const range = edit.range as Record<string, unknown>;
	if (
		!["start_line", "start_column", "end_line", "end_column"].every((key) =>
			Number.isInteger(range[key]),
		)
	) {
		return false;
	}
	const startLine = range.start_line as number;
	const startColumn = range.start_column as number;
	const endLine = range.end_line as number;
	const endColumn = range.end_column as number;
	return (
		startLine >= 1 &&
		startColumn >= 1 &&
		endLine >= startLine &&
		endColumn >= 1 &&
		(endLine !== startLine || endColumn >= startColumn)
	);
}

export function tabCacheKey(
	path: string,
	content: string,
	previous: string,
	line: number,
	column: number,
): string {
	return `${path}:${line}:${column}:${hashTabText(content)}:${hashTabText(previous)}`;
}

function hashTabText(value: string): string {
	let hash = 0x811c9dc5;
	for (let index = 0; index < value.length; index++) {
		hash ^= value.charCodeAt(index);
		hash = Math.imul(hash, 0x01000193);
	}
	return (hash >>> 0).toString(36);
}

function rememberTabPrediction(
	cache: Map<string, TabPredictionEdit | null>,
	key: string,
	edit: TabPredictionEdit | null,
) {
	cache.set(key, edit);
	if (cache.size <= 48) return;
	const oldest = cache.keys().next().value;
	if (oldest !== undefined) cache.delete(oldest);
}

function waitForTabDebounce(
	delay: number,
	token: MonacoTypes.CancellationToken,
): Promise<boolean> {
	if (token.isCancellationRequested) return Promise.resolve(false);
	return new Promise((resolve) => {
		let settled = false;
		let timer = 0;
		let cancellation: MonacoTypes.IDisposable | undefined;
		const finish = (ready: boolean) => {
			if (settled) return;
			settled = true;
			window.clearTimeout(timer);
			cancellation?.dispose();
			resolve(ready);
		};
		timer = window.setTimeout(() => finish(true), delay);
		cancellation = token.onCancellationRequested(() => finish(false));
	});
}
