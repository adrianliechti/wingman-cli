import type { Monaco } from "@monaco-editor/react";
import type * as MonacoTypes from "monaco-editor";
import {
	discoverDebugTargets,
	evaluateDebug,
	getDebugState,
	setDebugBreakpoints,
	type DebugAction,
	type DebugSourceBreakpoint,
	type DebugState,
	type DebugTarget,
} from "./api/debug";

interface DebugBridgeOptions {
	monaco: Monaco;
	editor: MonacoTypes.editor.IStandaloneCodeEditor;
	path: string;
	onLaunchTarget: (target: DebugTarget, action: DebugAction) => void;
}

export interface MonacoDebugBridge {
	dispose(): void;
}

let bridgeSequence = 0;

export function createMonacoDebugBridge({
	monaco,
	editor,
	path,
	onLaunchTarget,
}: DebugBridgeOptions): MonacoDebugBridge {
	const model = editor.getModel();
	const disposables: MonacoTypes.IDisposable[] = [];
	const requests = new Set<AbortController>();
	const breakpointDecorations = editor.createDecorationsCollection();
	const frameDecorations = editor.createDecorationsCollection();
	const sequence = ++bridgeSequence;
	const runCommand = `wingman.debug.run.${sequence}`;
	const debugCommand = `wingman.debug.start.${sequence}`;
	let disposed = false;
	let pollTimer = 0;
	let state: DebugState | null = null;
	let breakpoints: DebugSourceBreakpoint[] = [];
	let breakpointMutation = false;
	let breakpointRevision = 0;

	function track<T>(controller: AbortController, request: Promise<T>) {
		requests.add(controller);
		return request.finally(() => requests.delete(controller));
	}

	function applyDecorations() {
		if (!model || disposed) return;
		breakpointDecorations.set(
			breakpoints
				.filter((breakpoint) => breakpoint.line >= 1)
				.map((breakpoint) => ({
					range: new monaco.Range(breakpoint.line, 1, breakpoint.line, 1),
					options: {
						glyphMarginClassName: "wingman-debug-breakpoint",
						glyphMarginHoverMessage: { value: "Debug breakpoint" },
					},
				})),
		);

		const frame = state?.frame;
		if (
			state?.session?.state === "stopped" &&
			frame?.source?.path === path &&
			frame.line >= 1
		) {
			frameDecorations.set([
				{
					range: new monaco.Range(frame.line, 1, frame.line, 1),
					options: {
						isWholeLine: true,
						className: "wingman-debug-current-line",
						glyphMarginClassName: "wingman-debug-current-frame",
						glyphMarginHoverMessage: { value: "Current debug position" },
					},
				},
			]);
		} else {
			frameDecorations.clear();
		}
	}

	function applyState(next: DebugState) {
		state = next;
		if (!breakpointMutation) breakpoints = next.breakpoints ?? [];
		applyDecorations();
	}

	function schedulePoll() {
		window.clearTimeout(pollTimer);
		if (disposed) return;
		const delay = !state?.available
			? 4_000
			: state.session?.state === "running"
				? 700
				: state.session?.state === "stopped"
					? 2_000
					: 1_500;
		pollTimer = window.setTimeout(() => void refresh(), delay);
	}

	async function refresh() {
		if (disposed) return;
		const controller = new AbortController();
		try {
			const next = await track(
				controller,
				getDebugState(path, controller.signal),
			);
			if (!disposed) applyState(next);
		} catch {
			// Polling is best-effort. User actions surface their own errors in the
			// launcher; a transient adapter failure should not disturb editing.
		} finally {
			schedulePoll();
		}
	}

	async function toggleBreakpoint(line: number) {
		if (disposed || line < 1) return;
		const revision = ++breakpointRevision;
		const previous = breakpoints;
		const exists = previous.some((breakpoint) => breakpoint.line === line);
		breakpoints = exists
			? previous.filter((breakpoint) => breakpoint.line !== line)
			: [...previous, { line }].sort((left, right) => left.line - right.line);
		breakpointMutation = true;
		applyDecorations();
		const controller = new AbortController();
		try {
			const result = await track(
				controller,
				setDebugBreakpoints(path, breakpoints, controller.signal),
			);
			if (revision === breakpointRevision) breakpoints = result.breakpoints;
		} catch {
			if (revision === breakpointRevision) breakpoints = previous;
		} finally {
			if (revision === breakpointRevision) {
				breakpointMutation = false;
				applyDecorations();
			}
		}
	}

	disposables.push(
		monaco.editor.registerCommand(
			runCommand,
			(_accessor: unknown, target: DebugTarget) => {
				if (target?.id) onLaunchTarget(target, "run");
			},
		),
		monaco.editor.registerCommand(
			debugCommand,
			(_accessor: unknown, target: DebugTarget) => {
				if (target?.id) onLaunchTarget(target, "debug");
			},
		),
	);

	if (model) {
		const language = model.getLanguageId();
		disposables.push(
			monaco.languages.registerCodeLensProvider(language, {
				provideCodeLenses: async (
					candidate: MonacoTypes.editor.ITextModel,
					token: MonacoTypes.CancellationToken,
				) => {
					if (disposed || candidate !== model) {
						return { lenses: [], dispose() {} };
					}
					const controller = new AbortController();
					const cancellation = token.onCancellationRequested(() =>
						controller.abort(),
					);
					try {
						const result = await track(
							controller,
							discoverDebugTargets(
								path,
								candidate.getValue(),
								controller.signal,
							),
						);
						if (disposed || token.isCancellationRequested) {
							return { lenses: [], dispose() {} };
						}
						const lenses: MonacoTypes.languages.CodeLens[] = [];
						for (const target of result.targets) {
							const range = new monaco.Range(
								target.line,
								Math.max(1, target.column),
								target.line,
								Math.max(1, target.column),
							);
							lenses.push(
								{
									range,
									command: {
										id: runCommand,
										title: "Run",
										arguments: [target],
									},
								},
								{
									range,
									command: {
										id: debugCommand,
										title: "Debug",
										arguments: [target],
									},
								},
							);
						}
						return { lenses, dispose() {} };
					} catch {
						return { lenses: [], dispose() {} };
					} finally {
						cancellation.dispose();
					}
				},
			}),
			editor.onMouseDown((event) => {
				if (
					!event.event.leftButton ||
					event.event.rightButton ||
					event.target.type !==
						monaco.editor.MouseTargetType.GUTTER_GLYPH_MARGIN ||
					!event.target.position
				)
					return;
				void toggleBreakpoint(event.target.position.lineNumber);
			}),
			monaco.languages.registerHoverProvider(language, {
				provideHover: async (
					candidate: MonacoTypes.editor.ITextModel,
					position: MonacoTypes.Position,
					token: MonacoTypes.CancellationToken,
				) => {
					if (
						disposed ||
						candidate !== model ||
						state?.session?.state !== "stopped" ||
						!state.frame
					)
						return;
					const expression = expressionAt(editor, candidate, position);
					if (!expression) return;
					const controller = new AbortController();
					const cancellation = token.onCancellationRequested(() =>
						controller.abort(),
					);
					try {
						const evaluation = await track(
							controller,
							evaluateDebug(
								expression.text,
								state.session.session_id,
								state.frame.id,
								state.session.state_version,
								controller.signal,
							),
						);
						if (disposed || token.isCancellationRequested) return;
						const value = evaluation.result
							.slice(0, 4_000)
							.replaceAll("```", "` ` `");
						return {
							range: expression.range,
							contents: [
								{ value: `\`${escapeInlineCode(expression.text)}\`` },
								{ value: `\`\`\`text\n${value}\n\`\`\`` },
							],
						};
					} catch {
						return;
					} finally {
						cancellation.dispose();
					}
				},
			}),
		);
	}

	void refresh();

	return {
		dispose() {
			if (disposed) return;
			disposed = true;
			window.clearTimeout(pollTimer);
			for (const request of requests) request.abort();
			requests.clear();
			for (const disposable of disposables) disposable.dispose();
			breakpointDecorations.clear();
			frameDecorations.clear();
		},
	};
}

function expressionAt(
	editor: MonacoTypes.editor.IStandaloneCodeEditor,
	model: MonacoTypes.editor.ITextModel,
	position: MonacoTypes.Position,
) {
	const selection = editor.getSelection();
	if (
		selection &&
		!selection.isEmpty() &&
		selection.startLineNumber === selection.endLineNumber &&
		selection.containsPosition(position)
	) {
		const text = model.getValueInRange(selection).trim();
		if (text && text.length <= 240) return { text, range: selection };
	}
	const word = model.getWordAtPosition(position);
	if (!word) return null;
	return {
		text: word.word,
		range: {
			startLineNumber: position.lineNumber,
			startColumn: word.startColumn,
			endLineNumber: position.lineNumber,
			endColumn: word.endColumn,
		},
	};
}

function escapeInlineCode(value: string) {
	return value.replaceAll("`", "\\`");
}
