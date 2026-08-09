import type { Monaco } from "@monaco-editor/react";
import type * as MonacoTypes from "monaco-editor";
import type { DiagnosticEntry, FileContent } from "./types/protocol";

const markerOwner = "wingman-lsp";
const definitionScheme = "wingman-definition";

interface BridgeOptions {
	monaco: Monaco;
	editor: MonacoTypes.editor.IStandaloneCodeEditor;
	file: FileContent;
	getDirtyContent: () => string | undefined;
	onOpenFile?: (path: string, line: number, column: number) => void;
}

export interface MonacoLSPBridge {
	refreshDiagnostics(): Promise<void>;
	dispose(): void;
}

export function revealEditorPosition(
	editor: MonacoTypes.editor.ICodeEditor,
	line?: number,
	column?: number,
) {
	const model = editor.getModel();
	if (!model || !line || line < 1) return;
	const position = model.validatePosition({
		lineNumber: line,
		column: Math.max(1, column ?? 1),
	});
	editor.revealPositionInCenter(position);
	editor.setPosition(position);
	editor.focus();
}

export function createMonacoLSPBridge({
	monaco,
	editor,
	file,
	getDirtyContent,
	onOpenFile,
}: BridgeOptions): MonacoLSPBridge {
	const sourceModel = editor.getModel();
	const disposables: MonacoTypes.IDisposable[] = [];
	const ownedModels = new Map<string, MonacoTypes.editor.ITextModel>();
	const definitionPaths = new Map<string, string>();
	const requests = new Set<AbortController>();
	let disposed = false;
	let diagnosticsRevision = 0;
	let diagnosticsController: AbortController | null = null;

	async function trackedRequest<T>(
		token: MonacoTypes.CancellationToken,
		request: (signal: AbortSignal) => Promise<T | undefined>,
	): Promise<T | undefined> {
		if (disposed || token.isCancellationRequested) return;
		const controller = new AbortController();
		const cancellation = token.onCancellationRequested(() =>
			controller.abort(),
		);
		requests.add(controller);
		try {
			return await request(controller.signal);
		} catch {
			return;
		} finally {
			requests.delete(controller);
			cancellation.dispose();
		}
	}

	async function provideLocations(
		endpoint: string,
		model: MonacoTypes.editor.ITextModel,
		position: MonacoTypes.Position,
		token: MonacoTypes.CancellationToken,
	): Promise<MonacoTypes.languages.Location[] | undefined> {
		if (!sourceModel || model !== sourceModel) return;
		return trackedRequest(token, async (signal) => {
			const response = await fetch(endpoint, {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify(
					positionRequest(file.path, getDirtyContent(), position),
				),
				signal,
			});
			if (!response.ok || disposed || token.isCancellationRequested) return;
			const locations = (await response.json()) as LSPFileLocation[];
			const targetURIs = new Map<string, MonacoTypes.Uri>();
			for (const location of locations) {
				if (location.path === file.path) {
					targetURIs.set(location.path, sourceModel.uri);
					continue;
				}
				const uri = monaco.Uri.from({
					scheme: definitionScheme,
					path: `/${location.path}`,
				});
				targetURIs.set(location.path, uri);
				definitionPaths.set(uri.toString(), location.path);
			}

			async function loadTargetModel(
				targetPath: string,
				uri: MonacoTypes.Uri,
			): Promise<string | null> {
				if (targetPath === file.path) return targetPath;
				const fileResponse = await fetch(
					`/api/files/read?path=${encodeURIComponent(targetPath)}`,
					{ signal },
				);
				if (!fileResponse.ok || disposed || token.isCancellationRequested)
					return null;
				const targetFile = (await fileResponse.json()) as FileContent;
				if (targetFile.binary || targetFile.content === undefined) return null;

				const key = uri.toString();
				let targetModel = monaco.editor.getModel(uri);
				if (!targetModel) {
					targetModel = monaco.editor.createModel(
						targetFile.content,
						targetFile.language || undefined,
						uri,
					);
					ownedModels.set(key, targetModel);
				} else if (
					ownedModels.has(key) &&
					targetModel.getValue() !== targetFile.content
				) {
					targetModel.setValue(targetFile.content);
				}
				return targetPath;
			}

			const targets = Array.from(targetURIs);
			const loadedPaths: Array<string | null> = [];
			for (let index = 0; index < targets.length; index += 8) {
				loadedPaths.push(
					...(await Promise.all(
						targets
							.slice(index, index + 8)
							.map(([targetPath, uri]) => loadTargetModel(targetPath, uri)),
					)),
				);
				if (disposed || token.isCancellationRequested) return;
			}
			if (disposed || token.isCancellationRequested) return;

			const availablePaths = new Set(
				loadedPaths.filter((path): path is string => path !== null),
			);
			return locations
				.filter((location) => availablePaths.has(location.path))
				.map((location) => {
					const uri = targetURIs.get(location.path)!;
					const targetModel = monaco.editor.getModel(uri)!;
					const start = targetModel.validatePosition({
						lineNumber: location.line,
						column: location.column,
					});
					return {
						uri,
						range: wordRange(targetModel, start),
					};
				});
		});
	}

	if (sourceModel && file.language) {
		disposables.push(
			monaco.languages.registerDefinitionProvider(file.language, {
				provideDefinition: (
					model: MonacoTypes.editor.ITextModel,
					position: MonacoTypes.Position,
					token: MonacoTypes.CancellationToken,
				) => provideLocations("/api/lsp/definition", model, position, token),
			}),
			monaco.languages.registerTypeDefinitionProvider(file.language, {
				provideTypeDefinition: (
					model: MonacoTypes.editor.ITextModel,
					position: MonacoTypes.Position,
					token: MonacoTypes.CancellationToken,
				) =>
					provideLocations("/api/lsp/type-definition", model, position, token),
			}),
			monaco.languages.registerImplementationProvider(file.language, {
				provideImplementation: (
					model: MonacoTypes.editor.ITextModel,
					position: MonacoTypes.Position,
					token: MonacoTypes.CancellationToken,
				) =>
					provideLocations("/api/lsp/implementations", model, position, token),
			}),
			monaco.languages.registerReferenceProvider(file.language, {
				provideReferences: (
					model: MonacoTypes.editor.ITextModel,
					position: MonacoTypes.Position,
					_context: MonacoTypes.languages.ReferenceContext,
					token: MonacoTypes.CancellationToken,
				) => provideLocations("/api/lsp/references", model, position, token),
			}),
			monaco.languages.registerHoverProvider(file.language, {
				provideHover(
					model: MonacoTypes.editor.ITextModel,
					position: MonacoTypes.Position,
					token: MonacoTypes.CancellationToken,
				) {
					if (model !== sourceModel) return;
					return trackedRequest(token, async (signal) => {
						const response = await fetch("/api/lsp/hover", {
							method: "POST",
							headers: { "Content-Type": "application/json" },
							body: JSON.stringify(
								positionRequest(file.path, getDirtyContent(), position),
							),
							signal,
						});
						if (!response.ok) return;
						const result = (await response.json()) as { contents: string };
						return result.contents
							? { contents: [{ value: result.contents }] }
							: undefined;
					});
				},
			}),
			monaco.languages.registerDocumentSymbolProvider(file.language, {
				provideDocumentSymbols(
					model: MonacoTypes.editor.ITextModel,
					token: MonacoTypes.CancellationToken,
				) {
					if (model !== sourceModel) return;
					return trackedRequest(token, async (signal) => {
						const dirtyContent = getDirtyContent();
						const response = await fetch("/api/lsp/document-symbols", {
							method: "POST",
							headers: { "Content-Type": "application/json" },
							body: JSON.stringify({
								path: file.path,
								...(dirtyContent === undefined
									? {}
									: { content: dirtyContent }),
							}),
							signal,
						});
						if (!response.ok) return;
						const symbols = (await response.json()) as LSPDocumentSymbol[];
						return symbols.map((symbol) => documentSymbol(model, symbol));
					});
				},
			}),
		);
	}

	if (onOpenFile) {
		disposables.push(
			monaco.editor.registerEditorOpener({
				openCodeEditor(
					_source: MonacoTypes.editor.ICodeEditor,
					resource: MonacoTypes.Uri,
					selection?: MonacoTypes.IRange | MonacoTypes.IPosition,
				) {
					const targetPath = definitionPaths.get(resource.toString());
					if (!targetPath) return false;
					const line = selection
						? "lineNumber" in selection
							? selection.lineNumber
							: selection.startLineNumber
						: 1;
					const column = selection
						? "column" in selection
							? selection.column
							: selection.startColumn
						: 1;
					onOpenFile(targetPath, line, column);
					return true;
				},
			}),
		);
	}

	return {
		async refreshDiagnostics() {
			if (disposed || !sourceModel) return;
			diagnosticsController?.abort();
			const controller = new AbortController();
			diagnosticsController = controller;
			requests.add(controller);
			const revision = ++diagnosticsRevision;
			try {
				const dirtyContent = getDirtyContent();
				const response = await fetch("/api/lsp/diagnostics", {
					method: "POST",
					headers: { "Content-Type": "application/json" },
					body: JSON.stringify({
						path: file.path,
						...(dirtyContent === undefined ? {} : { content: dirtyContent }),
					}),
					signal: controller.signal,
				});
				if (!response.ok || disposed || revision !== diagnosticsRevision)
					return;
				const diagnostics = (await response.json()) as DiagnosticEntry[];
				if (disposed || revision !== diagnosticsRevision) return;
				monaco.editor.setModelMarkers(
					sourceModel,
					markerOwner,
					diagnostics
						.filter((diagnostic) => diagnostic.path === file.path)
						.map((diagnostic) =>
							diagnosticMarker(monaco, sourceModel, diagnostic),
						),
				);
			} catch {
				// Preserve the last known markers when the server is still analyzing.
			} finally {
				requests.delete(controller);
				if (diagnosticsController === controller) diagnosticsController = null;
			}
		},
		dispose() {
			if (disposed) return;
			disposed = true;
			diagnosticsRevision++;
			for (const controller of requests) controller.abort();
			requests.clear();
			diagnosticsController = null;
			if (sourceModel && !sourceModel.isDisposed()) {
				monaco.editor.setModelMarkers(sourceModel, markerOwner, []);
			}
			for (const disposable of disposables) disposable.dispose();
			for (const model of ownedModels.values()) model.dispose();
			ownedModels.clear();
			definitionPaths.clear();
		},
	};
}

interface LSPFileLocation {
	path: string;
	line: number;
	column: number;
}

interface LSPPosition {
	line: number;
	character: number;
}

interface LSPRange {
	start: LSPPosition;
	end: LSPPosition;
}

interface LSPDocumentSymbol {
	name: string;
	detail?: string;
	kind: number;
	range: LSPRange;
	selectionRange: LSPRange;
	children?: LSPDocumentSymbol[];
}

function positionRequest(
	path: string,
	content: string | undefined,
	position: MonacoTypes.Position,
) {
	return {
		path,
		...(content === undefined ? {} : { content }),
		line: position.lineNumber,
		column: position.column,
	};
}

function wordRange(
	model: MonacoTypes.editor.ITextModel,
	position: MonacoTypes.Position,
): MonacoTypes.IRange {
	const word = model.getWordAtPosition(position);
	const startColumn = word?.startColumn ?? position.column;
	const endColumn =
		word?.endColumn ??
		Math.min(position.column + 1, model.getLineMaxColumn(position.lineNumber));
	return {
		startLineNumber: position.lineNumber,
		startColumn,
		endLineNumber: position.lineNumber,
		endColumn,
	};
}

function documentSymbol(
	model: MonacoTypes.editor.ITextModel,
	symbol: LSPDocumentSymbol,
): MonacoTypes.languages.DocumentSymbol {
	return {
		name: symbol.name,
		detail: symbol.detail ?? "",
		kind: Math.max(0, symbol.kind - 1) as MonacoTypes.languages.SymbolKind,
		tags: [],
		range: lspRange(model, symbol.range),
		selectionRange: lspRange(model, symbol.selectionRange),
		children: symbol.children?.map((child) => documentSymbol(model, child)),
	};
}

function lspRange(
	model: MonacoTypes.editor.ITextModel,
	range: LSPRange,
): MonacoTypes.IRange {
	const start = model.validatePosition({
		lineNumber: range.start.line + 1,
		column: range.start.character + 1,
	});
	const end = model.validatePosition({
		lineNumber: range.end.line + 1,
		column: range.end.character + 1,
	});
	return {
		startLineNumber: start.lineNumber,
		startColumn: start.column,
		endLineNumber: end.lineNumber,
		endColumn: end.column,
	};
}

function diagnosticMarker(
	monaco: Monaco,
	model: MonacoTypes.editor.ITextModel,
	diagnostic: DiagnosticEntry,
): MonacoTypes.editor.IMarkerData {
	const start = model.validatePosition({
		lineNumber: diagnostic.line,
		column: diagnostic.column,
	});
	const end = model.validatePosition({
		lineNumber: Math.max(
			start.lineNumber,
			diagnostic.end_line ?? start.lineNumber,
		),
		column: diagnostic.end_column ?? start.column + 1,
	});
	return {
		startLineNumber: start.lineNumber,
		startColumn: start.column,
		endLineNumber: end.lineNumber,
		endColumn:
			end.lineNumber === start.lineNumber && end.column <= start.column
				? Math.min(start.column + 1, model.getLineMaxColumn(start.lineNumber))
				: end.column,
		message: diagnostic.message,
		source: diagnostic.source,
		severity:
			diagnostic.severity === "error"
				? monaco.MarkerSeverity.Error
				: diagnostic.severity === "warning"
					? monaco.MarkerSeverity.Warning
					: monaco.MarkerSeverity.Info,
	};
}
