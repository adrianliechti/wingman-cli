import type { Monaco } from "@monaco-editor/react";
import type * as MonacoTypes from "monaco-editor";
import type {
	CodeAction as LSPCodeAction,
	Command as LSPCommand,
	CompletionItem as LSPCompletionItem,
	CompletionList as LSPCompletionList,
	DocumentHighlight as LSPDocumentHighlight,
	DocumentSymbol as LSPDocumentSymbol,
	FoldingRange as LSPFoldingRange,
	InlayHint as LSPInlayHint,
	Location as LSPLocation,
	MarkupContent,
	Range as LSPRange,
	SignatureHelp as LSPSignatureHelp,
	TextEdit as LSPTextEdit,
} from "vscode-languageserver-types";
import type { DiagnosticEntry, FileContent } from "./types/protocol";
import type {
	WorkspaceDocumentSnapshot,
	WorkspaceEditEnvelope,
} from "./workspaceEdit";

const markerOwner = "wingman-lsp";
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
let bridgeSequence = 0;

interface BridgeOptions {
	monaco: Monaco;
	editor: MonacoTypes.editor.IStandaloneCodeEditor;
	file: FileContent;
	onCapabilitiesChanged?: () => void;
	onOpenFile?: (
		path: string,
		line: number,
		column: number,
		external?: boolean,
	) => void;
	onApplyWorkspaceEdit?: (
		envelope: WorkspaceEditEnvelope,
		label: string,
	) => Promise<boolean>;
}

export interface MonacoLSPBridge {
	refreshDiagnostics(): Promise<void>;
	organizeImports(): Promise<boolean>;
	supports(feature: MonacoLanguageFeature): boolean;
	dispose(): void;
}

export type MonacoLanguageFeature =
	| "definition"
	| "typeDefinition"
	| "implementation"
	| "references";

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
	onCapabilitiesChanged,
	onOpenFile,
	onApplyWorkspaceEdit,
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
	let workspaceURI = "";
	let activeCapabilities: LSPEditorCapabilities | null = null;
	let appliedCodeActions = 0;
	let resolveCapabilitiesReady!: () => void;
	const capabilitiesReady = new Promise<void>((resolve) => {
		resolveCapabilitiesReady = resolve;
	});
	const lspCommandID = `wingman.lsp.command.${++bridgeSequence}`;

	// Monaco's built-in workers publish diagnostics under the model language
	// ID. When this file has a real language server, keep diagnostics
	// project-aware by retaining only Wingman's LSP marker owner. This is done
	// per model so files without an active LSP keep Monaco's fallback checks.
	function suppressStandaloneDiagnostics() {
		if (
			!activeCapabilities?.language_server ||
			!sourceModel ||
			sourceModel.isDisposed()
		)
			return;
		const owner = sourceModel.getLanguageId();
		const hasStandaloneMarkers = monaco.editor
			.getModelMarkers({ resource: sourceModel.uri })
			.some((marker: MonacoTypes.editor.IMarker) => marker.owner === owner);
		if (hasStandaloneMarkers) {
			monaco.editor.setModelMarkers(sourceModel, owner, []);
		}
	}

	disposables.push(
		monaco.editor.onDidChangeMarkers(
			(resources: readonly MonacoTypes.Uri[]) => {
				if (
					sourceModel &&
					resources.some(
						(resource) => resource.toString() === sourceModel.uri.toString(),
					)
				) {
					suppressStandaloneDiagnostics();
				}
			},
		),
	);

	type CodeActionPayload = {
		action: LSPCodeAction | LSPCommand;
		documents: Record<string, WorkspaceDocumentSnapshot>;
		resolve: boolean;
	};

	async function executeLSPCommand(command: LSPCommand) {
		const controller = new AbortController();
		requests.add(controller);
		try {
			await postJSON(
				"/api/lsp/execute-command",
				{
					...documentRequest(file.path, sourceModel!),
					command,
				},
				controller.signal,
			);
		} finally {
			requests.delete(controller);
		}
	}

	async function runCodeAction(payload: CodeActionPayload): Promise<boolean> {
		if (!sourceModel || disposed) return false;
		let action = payload.action;
		let documents = payload.documents;
		if (
			payload.resolve &&
			!isLSPCommand(action) &&
			!action.edit &&
			action.data !== undefined
		) {
			const controller = new AbortController();
			requests.add(controller);
			try {
				const resolved = await postJSON<LSPResolvedCodeActionResponse>(
					"/api/lsp/code-actions/resolve",
					{
						...documentRequest(file.path, sourceModel),
						action,
					},
					controller.signal,
				);
				if (!resolved) return false;
				action = resolved.action;
				documents = resolved.documents;
			} finally {
				requests.delete(controller);
			}
		}
		if (!isLSPCommand(action) && action.edit) {
			const applied = await onApplyWorkspaceEdit?.(
				{ edit: action.edit, documents },
				action.title,
			);
			if (!applied) return false;
			appliedCodeActions++;
		}
		const command = isLSPCommand(action) ? action : action.command;
		if (command) await executeLSPCommand(command);
		return (!isLSPCommand(action) && action.edit !== undefined) || !!command;
	}

	async function waitForModelChange(version: number) {
		if (!sourceModel || sourceModel.getVersionId() !== version) return;
		await new Promise<void>((resolve) => {
			let settled = false;
			let timeout = 0;
			let listener: MonacoTypes.IDisposable | null = null;
			const finish = () => {
				if (settled) return;
				settled = true;
				window.clearTimeout(timeout);
				listener?.dispose();
				resolve();
			};
			listener = sourceModel!.onDidChangeContent(finish);
			timeout = window.setTimeout(finish, 750);
		});
	}

	async function runSourceAction(kind: string): Promise<boolean> {
		if (!sourceModel || disposed) return false;
		const controller = new AbortController();
		requests.add(controller);
		try {
			const response = await postJSON<LSPCodeActionsResponse>(
				"/api/lsp/code-actions",
				{
					...documentRequest(file.path, sourceModel),
					range: protocolRange(sourceModel.getFullModelRange()),
					only: [kind],
					trigger_kind: 1,
				},
				controller.signal,
			);
			if (!response || disposed) return false;
			for (const action of response.actions) {
				if (!isLSPCommand(action) && action.disabled) continue;
				const version = sourceModel.getVersionId();
				const executed = await runCodeAction({
					action,
					documents: response.documents,
					resolve: activeCapabilities?.code_action_resolve ?? false,
				});
				if (!executed) continue;
				await waitForModelChange(version);
				return true;
			}
			return false;
		} catch {
			return false;
		} finally {
			requests.delete(controller);
		}
	}

	disposables.push(
		monaco.editor.registerCommand(
			lspCommandID,
			(_accessor: unknown, payload: CodeActionPayload) =>
				runCodeAction(payload),
		),
	);

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
			const locations = await postJSON<LSPLocation[]>(
				endpoint,
				positionRequest(file.path, model, position),
				signal,
			);
			if (!locations || disposed || token.isCancellationRequested) return;
			const targetURIs = new Map<string, MonacoTypes.Uri>();
			for (const location of locations) {
				const target = lspLocationTarget(location.uri, workspaceURI);
				if (!target) continue;
				if (!target.external && target.path === file.path) {
					targetURIs.set(location.uri, sourceModel.uri);
					continue;
				}
				const uri = monaco.Uri.parse(location.uri);
				targetURIs.set(location.uri, uri);
				definitionTargets.set(uri.toString(), target);
			}

			async function loadTargetModel(
				targetURI: string,
				uri: MonacoTypes.Uri,
			): Promise<string | null> {
				const target = lspLocationTarget(targetURI, workspaceURI);
				if (!target) return null;
				if (!target.external && target.path === file.path) return targetURI;
				const fileResponse = await fetch(
					`${target.external ? "/api/lsp/file" : "/api/files/read"}?path=${encodeURIComponent(target.path)}`,
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
				return targetURI;
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

			const availableURIs = new Set(
				loadedPaths.filter((uri): uri is string => uri !== null),
			);
			return locations
				.filter((location) => availableURIs.has(location.uri))
				.map((location) => {
					const uri = targetURIs.get(location.uri)!;
					const targetModel = monaco.editor.getModel(uri)!;
					return {
						uri,
						range: lspRange(targetModel, location.range),
					};
				});
		});
	}

	async function provideFormattingEdits(
		endpoint: string,
		model: MonacoTypes.editor.ITextModel,
		body: unknown,
		token: MonacoTypes.CancellationToken,
	): Promise<MonacoTypes.languages.TextEdit[] | undefined> {
		return trackedRequest(token, async (signal) => {
			const edits = await postJSON<LSPTextEdit[]>(endpoint, body, signal);
			return edits?.map((edit) => ({
				range: lspRange(model, edit.range),
				text: edit.newText,
			}));
		});
	}

	function registerLanguageProviders(capabilities: LSPEditorCapabilities) {
		if (!sourceModel || !file.language || disposed) return;
		workspaceURI = capabilities.workspace_uri;

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
									lspCommandID,
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
											lspCommandID,
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
					provideDocumentHighlights(
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						token: MonacoTypes.CancellationToken,
					) {
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
					provideFoldingRanges(
						model: MonacoTypes.editor.ITextModel,
						_context: MonacoTypes.languages.FoldingContext,
						token: MonacoTypes.CancellationToken,
					) {
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
					provideDocumentSemanticTokens(
						model: MonacoTypes.editor.ITextModel,
						_lastResultID: string | null,
						token: MonacoTypes.CancellationToken,
					) {
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
		if (capabilities.rename && onApplyWorkspaceEdit) {
			disposables.push(
				monaco.languages.registerRenameProvider(file.language, {
					...(capabilities.rename_prepare
						? {
								resolveRenameLocation(
									model: MonacoTypes.editor.ITextModel,
									position: MonacoTypes.Position,
									token: MonacoTypes.CancellationToken,
								) {
									if (model !== sourceModel) return;
									return trackedRequest(token, async (signal) => {
										const result = await postJSON<LSPPrepareRenameResult>(
											"/api/lsp/rename/prepare",
											positionRequest(file.path, model, position),
											signal,
										);
										if (!result) return;
										if ("defaultBehavior" in result) {
											return {
												range: wordRange(model, position),
												text: "",
											};
										}
										const range = "range" in result ? result.range : result;
										const monacoRange = lspRange(model, range);
										return {
											range: monacoRange,
											text:
												"placeholder" in result
													? result.placeholder
													: model.getValueInRange(monacoRange),
										};
									});
								},
							}
						: {}),
					provideRenameEdits(
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						newName: string,
						token: MonacoTypes.CancellationToken,
					) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const response = await postJSON<WorkspaceEditEnvelope>(
								"/api/lsp/rename",
								{
									...positionRequest(file.path, model, position),
									new_name: newName,
								},
								signal,
							);
							if (!response?.edit)
								return { edits: [], rejectReason: "Rename is unavailable." };
							const applied = await onApplyWorkspaceEdit(
								response,
								`Rename to “${newName}”`,
							);
							return applied
								? { edits: [] }
								: { edits: [], rejectReason: "Rename was not applied." };
						});
					},
				}),
			);
		}
		if (capabilities.code_actions) {
			disposables.push(
				monaco.languages.registerCodeActionProvider(
					file.language,
					{
						provideCodeActions(
							model: MonacoTypes.editor.ITextModel,
							range: MonacoTypes.Range,
							context: MonacoTypes.languages.CodeActionContext,
							token: MonacoTypes.CancellationToken,
						) {
							if (model !== sourceModel) return;
							return trackedRequest(token, async (signal) => {
								const response = await postJSON<LSPCodeActionsResponse>(
									"/api/lsp/code-actions",
									{
										...documentRequest(file.path, model),
										range: protocolRange(range),
										...(context.only ? { only: [context.only] } : {}),
										trigger_kind: context.trigger,
									},
									signal,
								);
								if (!response) return;
								return {
									actions: response.actions.map((action) => {
										const resolvable =
											!isLSPCommand(action) &&
											capabilities.code_action_resolve &&
											action.data !== undefined;
										const executable =
											isLSPCommand(action) ||
											action.edit !== undefined ||
											action.command !== undefined ||
											resolvable;
										return {
											title: action.title,
											kind: isLSPCommand(action) ? undefined : action.kind,
											isPreferred: isLSPCommand(action)
												? undefined
												: action.isPreferred,
											disabled: isLSPCommand(action)
												? undefined
												: (action.disabled?.reason ??
													(!executable
														? "The language server did not provide an executable action."
														: undefined)),
											command: {
												id: lspCommandID,
												title: action.title,
												arguments: [
													{
														action,
														documents: response.documents,
														resolve: capabilities.code_action_resolve,
													},
												],
											},
										};
									}),
									dispose() {},
								};
							});
						},
					},
					{ providedCodeActionKinds: ["quickfix", "refactor", "source"] },
				),
			);
		}
		if (capabilities.document_formatting) {
			disposables.push(
				monaco.languages.registerDocumentFormattingEditProvider(file.language, {
					displayName: "Language Server",
					provideDocumentFormattingEdits(
						model: MonacoTypes.editor.ITextModel,
						options: MonacoTypes.languages.FormattingOptions,
						token: MonacoTypes.CancellationToken,
					) {
						if (model !== sourceModel) return;
						return provideFormattingEdits(
							"/api/lsp/formatting",
							model,
							{ ...documentRequest(file.path, model), options },
							token,
						);
					},
				}),
			);
		}
		if (capabilities.range_formatting) {
			disposables.push(
				monaco.languages.registerDocumentRangeFormattingEditProvider(
					file.language,
					{
						displayName: "Language Server",
						provideDocumentRangeFormattingEdits(
							model: MonacoTypes.editor.ITextModel,
							range: MonacoTypes.Range,
							options: MonacoTypes.languages.FormattingOptions,
							token: MonacoTypes.CancellationToken,
						) {
							if (model !== sourceModel) return;
							return provideFormattingEdits(
								"/api/lsp/formatting/range",
								model,
								{
									...documentRequest(file.path, model),
									options,
									range: protocolRange(range),
								},
								token,
							);
						},
					},
				),
			);
		}
		if (capabilities.on_type_formatting_trigger_characters.length > 0) {
			disposables.push(
				monaco.languages.registerOnTypeFormattingEditProvider(file.language, {
					autoFormatTriggerCharacters:
						capabilities.on_type_formatting_trigger_characters,
					provideOnTypeFormattingEdits(
						model: MonacoTypes.editor.ITextModel,
						position: MonacoTypes.Position,
						character: string,
						options: MonacoTypes.languages.FormattingOptions,
						token: MonacoTypes.CancellationToken,
					) {
						if (model !== sourceModel) return;
						return provideFormattingEdits(
							"/api/lsp/formatting/on-type",
							model,
							{
								...positionRequest(file.path, model, position),
								options,
								character,
							},
							token,
						);
					},
				}),
			);
		}
		if (capabilities.inlay_hints) {
			disposables.push(
				monaco.languages.registerInlayHintsProvider(file.language, {
					displayName: "Language Server",
					provideInlayHints(
						model: MonacoTypes.editor.ITextModel,
						range: MonacoTypes.Range,
						token: MonacoTypes.CancellationToken,
					) {
						if (model !== sourceModel) return;
						return trackedRequest(token, async (signal) => {
							const hints = await postJSON<LSPInlayHint[]>(
								"/api/lsp/inlay-hints",
								{
									...documentRequest(file.path, model),
									range: protocolRange(range),
								},
								signal,
							);
							if (!hints) return;
							return {
								hints: hints.map((hint) => ({
									position: {
										lineNumber: hint.position.line + 1,
										column: hint.position.character + 1,
									},
									label:
										typeof hint.label === "string"
											? hint.label
											: hint.label.map((part) => ({
													label: part.value,
													tooltip: documentation(part.tooltip),
													command: part.command
														? lspCommand(part.command, lspCommandID)
														: undefined,
												})),
									kind: hint.kind,
									tooltip: documentation(hint.tooltip),
									textEdits: hint.textEdits?.map((edit) => ({
										range: lspRange(model, edit.range),
										text: edit.newText,
									})),
									paddingLeft: hint.paddingLeft,
									paddingRight: hint.paddingRight,
								})),
								dispose() {},
							};
						});
					},
				}),
			);
		}

		activeCapabilities = capabilities;
		suppressStandaloneDiagnostics();
		onCapabilitiesChanged?.();
	}

	if (sourceModel && file.language) {
		const controller = new AbortController();
		requests.add(controller);
		void getEditorCapabilities(file.path, controller.signal)
			.then((capabilities) => registerLanguageProviders(capabilities))
			.catch(() => registerLanguageProviders(structuralCapabilities))
			.finally(() => {
				requests.delete(controller);
				resolveCapabilitiesReady();
			});
	} else {
		resolveCapabilitiesReady();
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
		async organizeImports() {
			await capabilitiesReady;
			if (disposed || !activeCapabilities?.code_actions || !sourceModel)
				return false;
			const before = appliedCodeActions;
			await runSourceAction("source.addMissingImports");
			await runSourceAction("source.organizeImports");
			return appliedCodeActions !== before;
		},
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
		supports(feature) {
			if (!activeCapabilities) return false;
			switch (feature) {
				case "definition":
					return activeCapabilities.definition;
				case "typeDefinition":
					return activeCapabilities.type_definition;
				case "implementation":
					return activeCapabilities.implementation;
				case "references":
					return activeCapabilities.references;
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

interface LSPSemanticToken {
	line: number;
	character: number;
	length: number;
	type: string;
	modifiers?: string[];
}

interface LSPFileTarget {
	path: string;
	external: boolean;
}

function lspLocationTarget(
	locationURI: string,
	workspaceURI: string,
): LSPFileTarget | null {
	const targetPath = fileURIPath(locationURI);
	const rootPath = fileURIPath(workspaceURI);
	if (!targetPath || !rootPath) return null;

	const normalizedRoot = rootPath.endsWith("/")
		? rootPath.slice(0, -1)
		: rootPath;
	const caseInsensitive = /^[a-z]:\//i.test(normalizedRoot);
	const comparableTarget = caseInsensitive
		? targetPath.toLowerCase()
		: targetPath;
	const comparableRoot = caseInsensitive
		? normalizedRoot.toLowerCase()
		: normalizedRoot;
	if (
		comparableTarget === comparableRoot ||
		comparableTarget.startsWith(`${comparableRoot}/`)
	) {
		return {
			path: targetPath.slice(normalizedRoot.length).replace(/^\/+/, ""),
			external: false,
		};
	}
	return { path: targetPath, external: true };
}

function fileURIPath(value: string): string | null {
	if (!value) return null;
	try {
		const uri = new URL(value);
		if (uri.protocol !== "file:") return null;
		let path = decodeURIComponent(uri.pathname).replaceAll("\\", "/");
		if (uri.hostname && uri.hostname !== "localhost") {
			path = `//${uri.hostname}${path}`;
		} else if (/^\/[a-z]:\//i.test(path)) {
			path = path.slice(1);
		}
		return path;
	} catch {
		return null;
	}
}

type LSPPrepareRenameResult =
	| LSPRange
	| { range: LSPRange; placeholder: string }
	| { defaultBehavior: boolean };

interface LSPCodeActionsResponse {
	actions: Array<LSPCodeAction | LSPCommand>;
	documents: Record<string, WorkspaceDocumentSnapshot>;
}

interface LSPResolvedCodeActionResponse {
	action: LSPCodeAction;
	documents: Record<string, WorkspaceDocumentSnapshot>;
}

interface LSPEditorCapabilities {
	workspace_uri: string;
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
	rename_prepare: boolean;
	code_actions: boolean;
	code_action_resolve: boolean;
	document_formatting: boolean;
	range_formatting: boolean;
	on_type_formatting_trigger_characters: string[];
	semantic_tokens: boolean;
	inlay_hints: boolean;
	workspace_symbols: boolean;
}

const structuralCapabilities: LSPEditorCapabilities = {
	workspace_uri: "",
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
	rename_prepare: false,
	code_actions: false,
	code_action_resolve: false,
	document_formatting: false,
	range_formatting: false,
	on_type_formatting_trigger_characters: [],
	semantic_tokens: true,
	inlay_hints: false,
	workspace_symbols: true,
};

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

function protocolRange(range: MonacoTypes.IRange): LSPRange {
	return {
		start: {
			line: range.startLineNumber - 1,
			character: range.startColumn - 1,
		},
		end: {
			line: range.endLineNumber - 1,
			character: range.endColumn - 1,
		},
	};
}

function isLSPCommand(
	action: LSPCodeAction | LSPCommand,
): action is LSPCommand {
	return typeof action.command === "string";
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
	commandID: string,
): MonacoTypes.languages.CompletionItem {
	const edit = item.textEdit;
	const range =
		edit && "insert" in edit
			? {
					insert: lspRange(model, edit.insert),
					replace: lspRange(model, edit.replace),
				}
			: edit
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
		command: item.command ? lspCommand(item.command, commandID) : undefined,
	};
}

function lspCommand(
	command: LSPCommand,
	commandID: string,
): MonacoTypes.languages.Command {
	return {
		id: commandID,
		title: command.title,
		arguments: [{ action: command, documents: {} }],
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
				activeParameter: signature.activeParameter ?? undefined,
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
			(token) => token.length > 0 && semanticTokenTypes.includes(token.type),
		)
		.toSorted(
			(a, b) =>
				a.line - b.line || a.character - b.character || a.length - b.length,
		);
	const data = new Uint32Array(sorted.length * 5);
	let previousLine = 0;
	let previousCharacter = 0;
	for (let index = 0; index < sorted.length; index++) {
		const token = sorted[index];
		const lineDelta = token.line - previousLine;
		const characterDelta =
			lineDelta === 0 ? token.character - previousCharacter : token.character;
		let modifierBits = 0;
		for (const modifier of token.modifiers ?? []) {
			const modifierIndex = semanticTokenModifiers.indexOf(modifier);
			if (modifierIndex >= 0) modifierBits |= 1 << modifierIndex;
		}
		data.set(
			[
				lineDelta,
				characterDelta,
				token.length,
				semanticTokenTypes.indexOf(token.type),
				modifierBits,
			],
			index * 5,
		);
		previousLine = token.line;
		previousCharacter = token.character;
	}
	return { data };
}

function documentation(value?: string | MarkupContent) {
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
