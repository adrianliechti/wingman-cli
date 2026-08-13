import type { Monaco } from "@monaco-editor/react";
import type * as MonacoTypes from "monaco-editor";
import type { DiagnosticEntry, FileContent } from "./types/protocol";

const markerOwner = "wingman-lsp";
const definitionScheme = "wingman-definition";
const semanticTokenTypes = [
	"namespace",
	"type",
	"class",
	"enum",
	"interface",
	"struct",
	"typeParameter",
	"parameter",
	"variable",
	"property",
	"enumMember",
	"event",
	"function",
	"method",
	"macro",
	"label",
	"comment",
	"string",
	"keyword",
	"number",
	"regexp",
	"operator",
	"decorator",
];
const semanticTokenModifiers = [
	"declaration",
	"definition",
	"readonly",
	"static",
	"deprecated",
	"abstract",
	"async",
	"modification",
	"documentation",
	"defaultLibrary",
];

interface BridgeOptions {
	monaco: Monaco;
	editor: MonacoTypes.editor.IStandaloneCodeEditor;
	file: FileContent;
	onOpenFile?: (
		path: string,
		line: number,
		column: number,
		external?: boolean,
	) => void;
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
	onOpenFile,
}: BridgeOptions): MonacoLSPBridge {
	const sourceModel = editor.getModel();
	const disposables: MonacoTypes.IDisposable[] = [];
	const ownedModels = new Map<string, MonacoTypes.editor.ITextModel>();
	const definitionTargets = new Map<
		string,
		{ path: string; external: boolean }
	>();
	const requests = new Set<AbortController>();
	const completionSources = new WeakMap<
		MonacoTypes.languages.CompletionItem,
		{
			item: LSPCompletionItem;
			model: MonacoTypes.editor.ITextModel;
			position: MonacoTypes.Position;
		}
	>();
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
			const locations = await postJSON<LSPFileLocation[]>(
				endpoint,
				positionRequest(file.path, model, position),
				signal,
			);
			if (!locations || disposed || token.isCancellationRequested) return;
			const targetURIs = new Map<string, MonacoTypes.Uri>();
			for (const location of locations) {
				if (location.path === file.path) {
					targetURIs.set(location.path, sourceModel.uri);
					continue;
				}
				const external = location.external === true;
				const uri = monaco.Uri.from({
					scheme: definitionScheme,
					path: location.path.startsWith("/")
						? location.path
						: `/${location.path}`,
				});
				targetURIs.set(location.path, uri);
				definitionTargets.set(uri.toString(), {
					path: location.path,
					external,
				});
			}

			async function loadTargetModel(
				targetPath: string,
				uri: MonacoTypes.Uri,
			): Promise<string | null> {
				if (targetPath === file.path) return targetPath;
				const external =
					definitionTargets.get(uri.toString())?.external === true;
				const fileResponse = await fetch(
					`${external ? "/api/lsp/file" : "/api/files/read"}?path=${encodeURIComponent(targetPath)}`,
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

	function registerLanguageProviders(capabilities: LSPEditorCapabilities) {
		if (!sourceModel || !file.language || disposed) return;

		if (capabilities.completion) {
			disposables.push(
				monaco.languages.registerCompletionItemProvider(file.language, {
					triggerCharacters: capabilities.completion_trigger_characters,
					provideCompletionItems(
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						context: MonacoTypes.languages.CompletionContext,
						token: MonacoTypes.CancellationToken,
					) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const list = await postJSON<LSPCompletionList>(
								"/api/lsp/completions",
								{
									...positionRequest(file.path, model, position),
									trigger_kind: context.triggerKind + 1,
									...(context.triggerCharacter
										? { trigger_character: context.triggerCharacter }
										: {}),
								},
								signal,
							);
							if (!list || disposed || token.isCancellationRequested) return;
							const suggestions = list.items.map((lspItem) => {
								const suggestion = completionItem(
									monaco,
									model,
									position,
									lspItem,
								);
								completionSources.set(suggestion, {
									item: lspItem,
									model,
									position,
								});
								return suggestion;
							});
							return {
								suggestions,
								incomplete: list.isIncomplete,
							};
						});
					},
					resolveCompletionItem: capabilities.completion_resolve
						? (
								suggestion: MonacoTypes.languages.CompletionItem,
								token: MonacoTypes.CancellationToken,
							) => {
								const source = completionSources.get(suggestion);
								if (!source) return suggestion;
								return trackedRequest(token, async (signal) => {
									const resolved = await postJSON<LSPCompletionItem>(
										"/api/lsp/completions/resolve",
										{
											...documentRequest(file.path, source.model),
											item: source.item,
										},
										signal,
									);
									if (!resolved) return suggestion;
									const enriched = {
										...suggestion,
										...completionItem(
											monaco,
											source.model,
											source.position,
											resolved,
										),
									};
									completionSources.set(enriched, {
										...source,
										item: resolved,
									});
									return enriched;
								});
							}
						: undefined,
				}),
			);
		}

		if (capabilities.signature_help) {
			disposables.push(
				monaco.languages.registerSignatureHelpProvider(file.language, {
					signatureHelpTriggerCharacters:
						capabilities.signature_help_trigger_characters,
					signatureHelpRetriggerCharacters:
						capabilities.signature_help_retrigger_characters,
					provideSignatureHelp(
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						token: MonacoTypes.CancellationToken,
						context: MonacoTypes.languages.SignatureHelpContext,
					) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const help = await postJSON<LSPSignatureHelp>(
								"/api/lsp/signature-help",
								{
									...positionRequest(file.path, model, position),
									trigger_kind: context.triggerKind,
									...(context.triggerCharacter
										? { trigger_character: context.triggerCharacter }
										: {}),
									is_retrigger: context.isRetrigger,
								},
								signal,
							);
							if (!help?.signatures.length || token.isCancellationRequested)
								return;
							return signatureHelp(help);
						});
					},
				}),
			);
		}

		if (capabilities.definition) {
			disposables.push(
				monaco.languages.registerDefinitionProvider(file.language, {
					provideDefinition: (
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						token: MonacoTypes.CancellationToken,
					) => provideLocations("/api/lsp/definition", model, position, token),
				}),
			);
		}
		if (capabilities.type_definition) {
			disposables.push(
				monaco.languages.registerTypeDefinitionProvider(file.language, {
					provideTypeDefinition: (
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						token: MonacoTypes.CancellationToken,
					) =>
						provideLocations(
							"/api/lsp/type-definition",
							model,
							position,
							token,
						),
				}),
			);
		}
		if (capabilities.implementation) {
			disposables.push(
				monaco.languages.registerImplementationProvider(file.language, {
					provideImplementation: (
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						token: MonacoTypes.CancellationToken,
					) =>
						provideLocations(
							"/api/lsp/implementations",
							model,
							position,
							token,
						),
				}),
			);
		}
		if (capabilities.references) {
			disposables.push(
				monaco.languages.registerReferenceProvider(file.language, {
					provideReferences: (
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						_context: MonacoTypes.languages.ReferenceContext,
						token: MonacoTypes.CancellationToken,
					) => provideLocations("/api/lsp/references", model, position, token),
				}),
			);
		}
		if (capabilities.hover) {
			disposables.push(
				monaco.languages.registerHoverProvider(file.language, {
					provideHover(
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						token: MonacoTypes.CancellationToken,
					) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const result = await postJSON<{ contents: string }>(
								"/api/lsp/hover",
								positionRequest(file.path, model, position),
								signal,
							);
							return result?.contents
								? { contents: [{ value: result.contents }] }
								: undefined;
						});
					},
				}),
			);
		}
		if (capabilities.document_symbols) {
			disposables.push(
				monaco.languages.registerDocumentSymbolProvider(file.language, {
					provideDocumentSymbols(
						model: MonacoTypes.editor.ITextModel,
						token: MonacoTypes.CancellationToken,
					) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const symbols = await postJSON<LSPDocumentSymbol[]>(
								"/api/lsp/document-symbols",
								documentRequest(file.path, model),
								signal,
							);
							return symbols?.map((symbol) => documentSymbol(model, symbol));
						});
					},
				}),
			);
		}
		if (capabilities.document_highlights) {
			disposables.push(
				monaco.languages.registerDocumentHighlightProvider(file.language, {
					provideDocumentHighlights(model, position, token) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const highlights = await postJSON<LSPDocumentHighlight[]>(
								"/api/lsp/document-highlights",
								positionRequest(file.path, model, position),
								signal,
							);
							return highlights?.map((highlight) => ({
								range: lspRange(model, highlight.range),
								kind: Math.max(
									0,
									(highlight.kind ?? 1) - 1,
								) as MonacoTypes.languages.DocumentHighlightKind,
							}));
						});
					},
				}),
			);
		}
		if (capabilities.folding_ranges) {
			disposables.push(
				monaco.languages.registerFoldingRangeProvider(file.language, {
					provideFoldingRanges(model, _context, token) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const ranges = await postJSON<LSPFoldingRange[]>(
								"/api/lsp/folding-ranges",
								documentRequest(file.path, model),
								signal,
							);
							return ranges?.map((range) => ({
								start: range.startLine + 1,
								end: range.endLine + 1,
								kind: foldingKind(monaco, range.kind),
							}));
						});
					},
				}),
			);
		}
		if (capabilities.semantic_tokens) {
			disposables.push(
				monaco.languages.registerDocumentSemanticTokensProvider(file.language, {
					getLegend: () => ({
						tokenTypes: semanticTokenTypes,
						tokenModifiers: semanticTokenModifiers,
					}),
					provideDocumentSemanticTokens(model, _lastResultID, token) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const tokens = await postJSON<LSPSemanticToken[]>(
								"/api/lsp/semantic-tokens",
								documentRequest(file.path, model),
								signal,
							);
							return tokens ? encodeSemanticTokens(tokens) : undefined;
						});
					},
					releaseDocumentSemanticTokens() {},
				}),
			);
		}
	}

	if (sourceModel && file.language) {
		const controller = new AbortController();
		requests.add(controller);
		void getEditorCapabilities(file.path, controller.signal)
			.then((capabilities) => registerLanguageProviders(capabilities))
			.catch(() => registerLanguageProviders(structuralCapabilities))
			.finally(() => requests.delete(controller));
	}

	if (onOpenFile) {
		disposables.push(
			monaco.editor.registerEditorOpener({
				openCodeEditor(
					_source: MonacoTypes.editor.ICodeEditor,
					resource: MonacoTypes.Uri,
					selection?: MonacoTypes.IRange | MonacoTypes.IPosition,
				) {
					const target = definitionTargets.get(resource.toString());
					if (!target) return false;
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
					onOpenFile(target.path, line, column, target.external);
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
				const diagnostics = await postJSON<DiagnosticEntry[]>(
					"/api/lsp/diagnostics",
					documentRequest(file.path, sourceModel),
					controller.signal,
				);
				if (!diagnostics || disposed || revision !== diagnosticsRevision)
					return;
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
			definitionTargets.clear();
		},
	};
}

interface LSPFileLocation {
	path: string;
	line: number;
	column: number;
	external?: boolean;
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

interface LSPDocumentHighlight {
	range: LSPRange;
	kind?: number;
}

interface LSPFoldingRange {
	startLine: number;
	startCharacter?: number;
	endLine: number;
	endCharacter?: number;
	kind?: string;
}

interface LSPSemanticToken {
	Line: number;
	Character: number;
	Length: number;
	Type: string;
	Modifiers?: string[];
}

interface LSPEditorCapabilities {
	language_server: boolean;
	completion: boolean;
	completion_resolve: boolean;
	completion_trigger_characters: string[];
	signature_help: boolean;
	signature_help_trigger_characters: string[];
	signature_help_retrigger_characters: string[];
	hover: boolean;
	definition: boolean;
	type_definition: boolean;
	references: boolean;
	implementation: boolean;
	document_symbols: boolean;
	document_highlights: boolean;
	folding_ranges: boolean;
	rename: boolean;
	code_actions: boolean;
	document_formatting: boolean;
	range_formatting: boolean;
	on_type_formatting_trigger_characters: string[];
	semantic_tokens: boolean;
	inlay_hints: boolean;
	workspace_symbols: boolean;
}

const structuralCapabilities: LSPEditorCapabilities = {
	language_server: false,
	completion: true,
	completion_resolve: false,
	completion_trigger_characters: [],
	signature_help: false,
	signature_help_trigger_characters: [],
	signature_help_retrigger_characters: [],
	hover: true,
	definition: true,
	type_definition: false,
	references: true,
	implementation: true,
	document_symbols: true,
	document_highlights: true,
	folding_ranges: true,
	rename: false,
	code_actions: false,
	document_formatting: false,
	range_formatting: false,
	on_type_formatting_trigger_characters: [],
	semantic_tokens: true,
	inlay_hints: false,
	workspace_symbols: true,
};

interface LSPCompletionList {
	isIncomplete: boolean;
	items: LSPCompletionItem[];
}

interface LSPCompletionItem {
	label: string;
	kind?: number;
	detail?: string;
	documentation?: string | { kind?: string; value?: string };
	sortText?: string;
	filterText?: string;
	insertText?: string;
	insertTextFormat?: number;
	textEdit?: LSPCompletionTextEdit;
	additionalTextEdits?: Array<{ range: LSPRange; newText: string }>;
	commitCharacters?: string[];
	preselect?: boolean;
	data?: unknown;
}

type LSPDocumentation = string | { kind?: string; value?: string };

interface LSPSignatureHelp {
	signatures: LSPSignatureInformation[];
	activeSignature?: number;
	activeParameter?: number;
}

interface LSPSignatureInformation {
	label: string;
	documentation?: LSPDocumentation;
	parameters?: LSPParameterInformation[];
	activeParameter?: number;
}

interface LSPParameterInformation {
	label: string | [number, number];
	documentation?: LSPDocumentation;
}

interface LSPCompletionTextEdit {
	newText: string;
	range?: LSPRange;
	insert?: LSPRange;
	replace?: LSPRange;
}

async function postJSON<T>(
	endpoint: string,
	body: unknown,
	signal: AbortSignal,
): Promise<T | undefined> {
	const response = await fetch(endpoint, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
		signal,
	});
	if (!response.ok) return undefined;
	return (await response.json()) as T;
}

async function getEditorCapabilities(
	path: string,
	signal: AbortSignal,
): Promise<LSPEditorCapabilities> {
	const response = await fetch(
		`/api/lsp/capabilities?path=${encodeURIComponent(path)}`,
		{ signal },
	);
	if (!response.ok) throw new Error("Editor capabilities unavailable");
	const capabilities = (await response.json()) as LSPEditorCapabilities;
	return {
		...capabilities,
		completion_trigger_characters:
			capabilities.completion_trigger_characters ?? [],
		signature_help_trigger_characters:
			capabilities.signature_help_trigger_characters ?? [],
		signature_help_retrigger_characters:
			capabilities.signature_help_retrigger_characters ?? [],
		on_type_formatting_trigger_characters:
			capabilities.on_type_formatting_trigger_characters ?? [],
	};
}

function documentRequest(path: string, model: MonacoTypes.editor.ITextModel) {
	return { path, content: model.getValue() };
}

function positionRequest(
	path: string,
	model: MonacoTypes.editor.ITextModel,
	position: MonacoTypes.Position,
) {
	return {
		...documentRequest(path, model),
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

function completionItem(
	monaco: Monaco,
	model: MonacoTypes.editor.ITextModel,
	position: MonacoTypes.Position,
	item: LSPCompletionItem,
): MonacoTypes.languages.CompletionItem {
	const edit = item.textEdit;
	const range =
		edit?.insert && edit.replace
			? {
					insert: lspRange(model, edit.insert),
					replace: lspRange(model, edit.replace),
				}
			: edit?.range
				? lspRange(model, edit.range)
				: wordRange(model, position);
	return {
		label: item.label,
		kind: completionKind(monaco, item.kind),
		detail: item.detail,
		documentation: documentation(item.documentation),
		sortText: item.sortText,
		filterText: item.filterText,
		insertText: edit?.newText ?? item.insertText ?? item.label,
		insertTextRules:
			item.insertTextFormat === 2
				? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet
				: undefined,
		range,
		additionalTextEdits: item.additionalTextEdits?.map((additional) => ({
			range: lspRange(model, additional.range),
			text: additional.newText,
		})),
		commitCharacters: item.commitCharacters,
		preselect: item.preselect,
	};
}

function signatureHelp(
	help: LSPSignatureHelp,
): MonacoTypes.languages.SignatureHelpResult {
	return {
		value: {
			signatures: help.signatures.map((signature) => ({
				label: signature.label,
				documentation: documentation(signature.documentation),
				parameters: (signature.parameters ?? []).map((parameter) => ({
					label: parameter.label,
					documentation: documentation(parameter.documentation),
				})),
				activeParameter: signature.activeParameter,
			})),
			activeSignature: help.activeSignature ?? 0,
			activeParameter: help.activeParameter ?? 0,
		},
		dispose() {},
	};
}

function foldingKind(monaco: Monaco, kind?: string) {
	switch (kind) {
		case "comment":
			return monaco.languages.FoldingRangeKind.Comment;
		case "imports":
			return monaco.languages.FoldingRangeKind.Imports;
		case "region":
			return monaco.languages.FoldingRangeKind.Region;
	}
}

function encodeSemanticTokens(
	tokens: LSPSemanticToken[],
): MonacoTypes.languages.SemanticTokens {
	const sorted = tokens
		.filter(
			(token) =>
				token.Length > 0 && semanticTokenTypes.includes(token.Type),
		)
		.toSorted(
			(a, b) => a.Line - b.Line || a.Character - b.Character || a.Length - b.Length,
		);
	const data = new Uint32Array(sorted.length * 5);
	let previousLine = 0;
	let previousCharacter = 0;
	for (let index = 0; index < sorted.length; index++) {
		const token = sorted[index];
		const lineDelta = token.Line - previousLine;
		const characterDelta =
			lineDelta === 0 ? token.Character - previousCharacter : token.Character;
		let modifierBits = 0;
		for (const modifier of token.Modifiers ?? []) {
			const modifierIndex = semanticTokenModifiers.indexOf(modifier);
			if (modifierIndex >= 0) modifierBits |= 1 << modifierIndex;
		}
		data.set(
			[
				lineDelta,
				characterDelta,
				token.Length,
				semanticTokenTypes.indexOf(token.Type),
				modifierBits,
			],
			index * 5,
		);
		previousLine = token.Line;
		previousCharacter = token.Character;
	}
	return { data };
}

function documentation(value?: LSPDocumentation) {
	const text = typeof value === "string" ? value : value?.value;
	return text ? { value: text } : undefined;
}

function completionKind(
	monaco: Monaco,
	kind?: number,
): MonacoTypes.languages.CompletionItemKind {
	const kinds = monaco.languages.CompletionItemKind;
	return (
		[
			kinds.Text,
			kinds.Method,
			kinds.Function,
			kinds.Constructor,
			kinds.Field,
			kinds.Variable,
			kinds.Class,
			kinds.Interface,
			kinds.Module,
			kinds.Property,
			kinds.Unit,
			kinds.Value,
			kinds.Enum,
			kinds.Keyword,
			kinds.Snippet,
			kinds.Color,
			kinds.File,
			kinds.Reference,
			kinds.Folder,
			kinds.EnumMember,
			kinds.Constant,
			kinds.Struct,
			kinds.Event,
			kinds.Operator,
			kinds.TypeParameter,
		][Math.max(0, (kind ?? 1) - 1)] ?? kinds.Text
	);
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
