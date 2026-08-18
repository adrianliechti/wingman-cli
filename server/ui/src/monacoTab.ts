import type { Monaco } from "@monaco-editor/react";
import type * as MonacoTypes from "monaco-editor";

const MAX_DOCUMENT_LENGTH = 1 << 20;
const BURST_GAP_MS = 1_000;
const RECENT_EDIT_MS = 10_000;
const REQUEST_DEBOUNCE_MS = 350;
// This is start-to-start. A normal request taking longer than 1.5 seconds adds
// no further delay before the next eligible request.
const MIN_REQUEST_GAP_MS = 1_500;
// Ordinary typing and cursor movement cancel stale work much sooner. This is
// only a transport safety valve for a request whose editor state stayed valid.
const REQUEST_TIMEOUT_MS = 6_000;

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
	const cursorTrigger =
		new monaco.Emitter<MonacoTypes.languages.IInlineCompletionChangeHint | void>();
	let disposed = false;
	let engaged = false;
	let previousValue = model.getValue();
	let burstBefore = previousValue;
	let lastChangeAt = 0;
	let lastRequestAt = 0;
	let activeController: AbortController | null = null;
	let activeRequestKey: string | null = null;
	let postAcceptScheduled = false;

	function abortActiveRequest() {
		activeController?.abort();
		activeController = null;
	}

	const handleAccepted = () => {
		if (disposed || postAcceptScheduled) return;
		const current = model.getValue();
		engaged = false;
		burstBefore = current;
		previousValue = current;
		abortActiveRequest();
		postAcceptScheduled = true;
		window.setTimeout(() => {
			postAcceptScheduled = false;
			if (!disposed) void onAccepted?.();
		}, 0);
	};

	disposables.push(
		model.onDidChangeContent((event) => {
			const current = model.getValue();
			abortActiveRequest();
			if (replacesEntireTabDocument(event, previousValue.length)) {
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
		editor.onDidChangeCursorPosition((event) => {
			if (disposed || !engaged) return;
			if (Date.now() - lastChangeAt > RECENT_EDIT_MS) {
				engaged = false;
				return;
			}
			if (
				event.source === "inlineCompletions.jump" ||
				event.source === "inlineCompletionAccept"
			)
				return;
			abortActiveRequest();
			// Let Monaco publish the new selection before asking this provider to
			// refresh; the event itself owns debouncing and cancellation.
			window.setTimeout(() => {
				if (!disposed) cursorTrigger.fire({ data: { reason: "cursor" } });
			}, 0);
		}),
		cursorTrigger,
	);

	disposables.push(
		monaco.languages.registerInlineCompletionsProvider(model.getLanguageId(), {
			groupId: "wingman.tab",
			displayName: "Wingman Tab",
			debounceDelayMs: REQUEST_DEBOUNCE_MS,
			onDidChangeInlineCompletions: cursorTrigger.event,
			async provideInlineCompletions(
				candidateModel: MonacoTypes.editor.ITextModel,
				position: MonacoTypes.Position,
				context: MonacoTypes.languages.InlineCompletionContext,
				token: MonacoTypes.CancellationToken,
			): Promise<TabInlineCompletions> {
				const empty = emptyTabCompletions();
				if (engaged && Date.now() - lastChangeAt > RECENT_EDIT_MS)
					engaged = false;
				if (
					disposed ||
					candidateModel !== model ||
					!engaged ||
					candidateModel.getValueLength() > MAX_DOCUMENT_LENGTH ||
					context.selectedSuggestionInfo
				) {
					return empty;
				}
				const selection = editor.getSelection();
				if (!selection?.isEmpty()) return empty;

				const content = candidateModel.getValue();
				if (content === burstBefore) return empty;
				const version = candidateModel.getVersionId();
				const key = tabCacheKey(
					path,
					content,
					burstBefore,
					position.lineNumber,
					position.column,
				);
				if (activeRequestKey === key) return empty;
				if (!cache.has(key)) {
					const requestGap = Math.max(
						0,
						MIN_REQUEST_GAP_MS - (Date.now() - lastRequestAt),
					);
					if (requestGap > 0 && !(await waitForTabDelay(requestGap, token)))
						return empty;
					if (
						disposed ||
						!engaged ||
						activeRequestKey === key ||
						candidateModel.getVersionId() !== version ||
						candidateModel.getValue() !== content
					)
						return empty;
				}

				let edit = cache.get(key);
				if (edit === undefined) {
					const controller = new AbortController();
					abortActiveRequest();
					activeController = controller;
					activeRequestKey = key;
					lastRequestAt = Date.now();
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
						if (activeRequestKey === key) activeRequestKey = null;
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
				const isInlineEdit =
					!insertionAtCursor || edit.insert_text.includes("\n");
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
				}
			},
			disposeInlineCompletions() {},
		}),
	);

	return {
		dispose() {
			if (disposed) return;
			disposed = true;
			abortActiveRequest();
			for (const disposable of disposables) disposable.dispose();
			cache.clear();
		},
	};
}

interface TabContentChangeEvent {
	isFlush: boolean;
	changes: readonly { rangeOffset: number; rangeLength: number }[];
}

export function replacesEntireTabDocument(
	event: TabContentChangeEvent,
	previousLength: number,
): boolean {
	if (event.isFlush || event.changes.length !== 1) return event.isFlush;
	const [change] = event.changes;
	return change.rangeOffset === 0 && change.rangeLength === previousLength;
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

function waitForTabDelay(
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
