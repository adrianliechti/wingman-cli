import Editor, { type Monaco, type OnMount } from "@monaco-editor/react";
import { AlertTriangle, FileDigit, Loader2 } from "lucide-react";
import {
	forwardRef,
	useCallback,
	useEffect,
	useImperativeHandle,
	useLayoutEffect,
	useRef,
	useState,
} from "react";
import type { DebugAction, DebugTarget } from "../api/debug";
import {
	transformEditorSelection,
	type EditorTransformRange,
} from "../api/editor";
import {
	workspaceFileDownloadURL,
	workspaceFilePreviewURL,
} from "../api/files";
import { registerEditorSaveParticipant } from "../editorSaveParticipants";
import { useColorScheme } from "../hooks/useColorScheme";
import type { OpenDocument, SaveResult } from "../hooks/useOpenDocuments";
import {
	createMonacoLSPBridge,
	type MonacoLSPBridge,
	revealEditorPosition,
} from "../monacoLsp";
import {
	createMonacoDebugBridge,
	type MonacoDebugBridge,
} from "../monacoDebug";
import { createMonacoTabBridge, type MonacoTabBridge } from "../monacoTab";
import {
	createMonacoTransformBridge,
	isEditorTransformResponse,
	type MonacoTransformBridge,
} from "../monacoTransform";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type { ServerMessage } from "../types/protocol";
import type { WorkspaceEditEnvelope } from "../workspaceEdit";
import { textPreviewKind } from "../utils/filePreview";
import { DataPreview } from "./DataPreview";
import { EditorContextMenu } from "./EditorContextMenu";
import { InlineTransformPrompt } from "./InlineTransformPrompt";
import { MarkdownContent } from "./MarkdownContent";
import { MermaidPreview } from "./MermaidPreview";
import { useToast } from "./ui/Feedback";

interface Props {
	document: OpenDocument;
	line?: number;
	column?: number;
	navigationKey?: number;
	subscribe?: (handler: (message: ServerMessage) => void) => () => void;
	onChange: (value: string) => void;
	onSave: () => Promise<SaveResult>;
	onReload: () => void;
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
	onLaunchDebug?: (target: DebugTarget, action: DebugAction) => void;
	onAskSelection?: (selection: EditorSelectionContext) => void;
	view?: "code" | "preview";
	tabEnabled?: boolean;
	languageServicesKey?: string;
}

export interface EditorSelectionContext {
	path: string;
	language: string;
	range: EditorTransformRange;
	text: string;
}

export interface FileTabHandle {
	hasSelection: () => boolean;
	chatAboutSelection: () => boolean;
	transformSelection: () => boolean;
}

interface TransformTarget {
	reference: { x: number; y: number };
	range: EditorTransformRange;
	expectedText: string;
	version: number;
	label: string;
}

type CodeEditor = Parameters<OnMount>[0];

function synchronizeEditorDraft(editor: CodeEditor, next: string) {
	const model = editor.getModel();
	if (!model) return;
	const current = model.getValue();
	if (current === next) return;

	let prefix = 0;
	while (
		prefix < current.length &&
		prefix < next.length &&
		current.charCodeAt(prefix) === next.charCodeAt(prefix)
	) {
		prefix++;
	}
	let suffix = 0;
	while (
		suffix < current.length - prefix &&
		suffix < next.length - prefix &&
		current.charCodeAt(current.length - suffix - 1) ===
			next.charCodeAt(next.length - suffix - 1)
	) {
		suffix++;
	}

	const oldEnd = current.length - suffix;
	const replacement = next.slice(prefix, next.length - suffix);
	const newEnd = prefix + replacement.length;
	const startPosition = model.getPositionAt(prefix);
	const endPosition = model.getPositionAt(oldEnd);
	const selections = editor.getSelections()?.map((selection) => ({
		anchor: model.getOffsetAt({
			lineNumber: selection.selectionStartLineNumber,
			column: selection.selectionStartColumn,
		}),
		position: model.getOffsetAt({
			lineNumber: selection.positionLineNumber,
			column: selection.positionColumn,
		}),
	}));
	const scrollTop = editor.getScrollTop();
	const scrollLeft = editor.getScrollLeft();
	const mapOffset = (offset: number) => {
		if (offset <= prefix) return offset;
		if (offset >= oldEnd) return newEnd + offset - oldEnd;
		return prefix + Math.min(offset - prefix, replacement.length);
	};

	editor.pushUndoStop();
	editor.executeEdits("wingman.workspaceEdit", [
		{
			range: {
				startLineNumber: startPosition.lineNumber,
				startColumn: startPosition.column,
				endLineNumber: endPosition.lineNumber,
				endColumn: endPosition.column,
			},
			text: replacement,
			forceMoveMarkers: true,
		},
	]);
	editor.pushUndoStop();
	if (selections) {
		editor.setSelections(
			selections.map((selection) => {
				const anchor = model.getPositionAt(mapOffset(selection.anchor));
				const position = model.getPositionAt(mapOffset(selection.position));
				return {
					selectionStartLineNumber: anchor.lineNumber,
					selectionStartColumn: anchor.column,
					positionLineNumber: position.lineNumber,
					positionColumn: position.column,
				};
			}),
		);
	}
	editor.setScrollPosition({ scrollTop, scrollLeft });
}

export const FileTab = forwardRef<FileTabHandle, Props>(function FileTab(
	{
		document,
		line,
		column,
		navigationKey,
		subscribe,
		onChange,
		onSave,
		onReload,
		onOpenFile,
		onApplyWorkspaceEdit,
		onLaunchDebug,
		onAskSelection,
		view = "code",
		tabEnabled = false,
		languageServicesKey = "",
	},
	ref,
) {
	const editorRef = useRef<Parameters<OnMount>[0] | null>(null);
	const monacoRef = useRef<Monaco | null>(null);
	const contextMenuListenerRef = useRef<{ dispose(): void } | null>(null);
	const saveParticipantDisposeRef = useRef<(() => void) | null>(null);
	const lspBridgeRef = useRef<MonacoLSPBridge | null>(null);
	const debugBridgeRef = useRef<MonacoDebugBridge | null>(null);
	const tabBridgeRef = useRef<MonacoTabBridge | null>(null);
	const transformBridgeRef = useRef<MonacoTransformBridge | null>(null);
	const diagnosticsTimerRef = useRef<number | null>(null);
	const onOpenFileRef = useRef(onOpenFile);
	const onApplyWorkspaceEditRef = useRef(onApplyWorkspaceEdit);
	const onLaunchDebugRef = useRef(onLaunchDebug);
	const onAskSelectionRef = useRef(onAskSelection);
	const onSaveRef = useRef(onSave);
	const languageServicesKeyRef = useRef(languageServicesKey);
	const syncingEditorRef = useRef(false);
	const scheme = useColorScheme();
	const toast = useToast();
	const [contextMenu, setContextMenu] = useState<{
		x: number;
		y: number;
		altKey: boolean;
		editor: Parameters<OnMount>[0];
	} | null>(null);
	const [transformTarget, setTransformTarget] =
		useState<TransformTarget | null>(null);
	const [, setLanguageFeaturesRevision] = useState(0);

	useEffect(() => {
		onOpenFileRef.current = onOpenFile;
		onApplyWorkspaceEditRef.current = onApplyWorkspaceEdit;
		onLaunchDebugRef.current = onLaunchDebug;
		onAskSelectionRef.current = onAskSelection;
		onSaveRef.current = onSave;
	}, [onApplyWorkspaceEdit, onAskSelection, onLaunchDebug, onOpenFile, onSave]);

	const dirty = document.draft !== document.savedContent;
	const file = document.file;
	const disposeLSPIntegration = useCallback(() => {
		saveParticipantDisposeRef.current?.();
		saveParticipantDisposeRef.current = null;
		lspBridgeRef.current?.dispose();
		lspBridgeRef.current = null;
	}, []);
	const disposeEditorIntegration = useCallback(() => {
		contextMenuListenerRef.current?.dispose();
		contextMenuListenerRef.current = null;
		disposeLSPIntegration();
		debugBridgeRef.current?.dispose();
		debugBridgeRef.current = null;
		transformBridgeRef.current?.dispose();
		transformBridgeRef.current = null;
		tabBridgeRef.current?.dispose();
		tabBridgeRef.current = null;
		editorRef.current = null;
		monacoRef.current = null;
	}, [disposeLSPIntegration]);
	const refreshTabIntegration = useCallback(() => {
		tabBridgeRef.current?.dispose();
		tabBridgeRef.current = null;
		if (
			!tabEnabled ||
			document.external ||
			!file ||
			!editorRef.current ||
			!monacoRef.current
		) {
			return;
		}
		tabBridgeRef.current = createMonacoTabBridge({
			monaco: monacoRef.current,
			editor: editorRef.current,
			path: file.path,
		});
	}, [document.external, file, tabEnabled]);

	const selectionTarget = (
		editor: Parameters<OnMount>[0],
	): TransformTarget | null => {
		if (document.external || !file) return null;
		const model = editor.getModel();
		const selection = editor.getSelection();
		if (!model || !selection || selection.isEmpty()) return null;
		const start = selection.getStartPosition();
		const end = selection.getEndPosition();
		const visible = editor.getScrolledVisiblePosition(end);
		const bounds = editor.getDomNode()?.getBoundingClientRect();
		if (!bounds) return null;
		return {
			reference: {
				x: bounds.left + (visible?.left ?? 8),
				y: bounds.top + (visible?.top ?? 8) + (visible?.height ?? 16),
			},
			range: {
				start_line: start.lineNumber,
				start_column: start.column,
				end_line: end.lineNumber,
				end_column: end.column,
			},
			expectedText: model.getValueInRange(selection),
			version: model.getVersionId(),
			label:
				start.lineNumber === end.lineNumber
					? `${file.path}:${start.lineNumber}`
					: `${file.path}:${start.lineNumber}-${end.lineNumber}`,
		};
	};

	const openTransformPrompt = (editor: Parameters<OnMount>[0]) => {
		const target = selectionTarget(editor);
		if (!target) return;
		setContextMenu(null);
		setTransformTarget(target);
	};

	const askWithSelectionTarget = (target: TransformTarget) => {
		if (!file) return;
		onAskSelectionRef.current?.({
			path: file.path,
			language: file.language ?? "",
			range: target.range,
			text: target.expectedText,
		});
		setContextMenu(null);
		setTransformTarget(null);
	};

	const askAboutSelection = (editor: Parameters<OnMount>[0] | null) => {
		if (!editor) return;
		const target = selectionTarget(editor);
		if (target) askWithSelectionTarget(target);
	};

	useImperativeHandle(ref, () => ({
		hasSelection: () => {
			if (document.external || !file || view !== "code") return false;
			const selection = editorRef.current?.getSelection();
			return !!selection && !selection.isEmpty();
		},
		chatAboutSelection: () => {
			const editor = editorRef.current;
			if (!editor || !selectionTarget(editor)) return false;
			askAboutSelection(editor);
			return true;
		},
		transformSelection: () => {
			const editor = editorRef.current;
			if (!editor || !selectionTarget(editor)) return false;
			openTransformPrompt(editor);
			return true;
		},
	}));

	useEffect(() => {
		const editor = editorRef.current;
		if (!editor || !line || line < 1) return;
		revealEditorPosition(editor, line, column);
	}, [line, column, navigationKey]);

	useEffect(() => {
		return () => {
			if (diagnosticsTimerRef.current !== null) {
				window.clearTimeout(diagnosticsTimerRef.current);
			}
			disposeEditorIntegration();
		};
	}, [disposeEditorIntegration]);

	useEffect(() => {
		refreshTabIntegration();
		return () => {
			tabBridgeRef.current?.dispose();
			tabBridgeRef.current = null;
		};
	}, [refreshTabIntegration]);

	useEffect(() => {
		if (view !== "code") {
			disposeEditorIntegration();
		}
	}, [disposeEditorIntegration, view]);

	const loadDiagnostics = useCallback(async () => {
		await lspBridgeRef.current?.refreshDiagnostics();
	}, []);
	const refreshLSPIntegration = useCallback(() => {
		disposeLSPIntegration();
		const editor = editorRef.current;
		const monaco = monacoRef.current;
		if (document.external || !file || !editor || !monaco) return;

		const bridge = createMonacoLSPBridge({
			monaco,
			editor,
			file,
			onCapabilitiesChanged: () =>
				setLanguageFeaturesRevision((revision) => revision + 1),
			onOpenFile: (path, row, col, external) =>
				onOpenFileRef.current?.(path, row, col, external),
			onApplyWorkspaceEdit: (envelope, label) =>
				onApplyWorkspaceEditRef.current?.(envelope, label) ??
				Promise.resolve(false),
			onCommandError: (label, error) =>
				toast({
					title: `${label} failed`,
					description: error instanceof Error ? error.message : String(error),
					tone: "error",
				}),
		});
		lspBridgeRef.current = bridge;
		saveParticipantDisposeRef.current = registerEditorSaveParticipant(
			file.path,
			() => bridge.organizeImports(),
		);
		void bridge.refreshDiagnostics();
	}, [disposeLSPIntegration, document.external, file, toast]);

	useEffect(() => {
		if (languageServicesKeyRef.current === languageServicesKey) return;
		languageServicesKeyRef.current = languageServicesKey;
		refreshLSPIntegration();
	}, [languageServicesKey, refreshLSPIntegration]);

	useEffect(() => {
		if (!file || file.binary || !editorRef.current) return;
		const timer = window.setTimeout(
			() => void loadDiagnostics(),
			dirty ? 600 : 0,
		);
		return () => window.clearTimeout(timer);
	}, [dirty, document.draft, file, loadDiagnostics]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((message) => {
			if (message.type !== "diagnostics_changed") return;
			if (diagnosticsTimerRef.current !== null) {
				window.clearTimeout(diagnosticsTimerRef.current);
			}
			diagnosticsTimerRef.current = window.setTimeout(() => {
				diagnosticsTimerRef.current = null;
				void loadDiagnostics();
			}, 200);
		});
	}, [loadDiagnostics, subscribe]);

	useLayoutEffect(() => {
		const editor = editorRef.current;
		if (!editor || view !== "code") return;
		syncingEditorRef.current = true;
		try {
			synchronizeEditorDraft(editor, document.draft);
		} finally {
			syncingEditorRef.current = false;
		}
	}, [document.draft, view]);

	if (document.loading && !file) {
		return (
			<div className="flex h-full items-center justify-center text-fg-dim">
				<Loader2 size={15} className="animate-spin" aria-label="Loading file" />
			</div>
		);
	}

	if (!file) {
		return (
			<div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
				<div className="max-w-md text-[12px] text-danger">
					{document.error || "Failed to load file."}
				</div>
				<button
					type="button"
					onClick={onReload}
					className="h-8 rounded-md border border-border px-3 text-[12px] text-fg-muted hover:bg-bg-hover hover:text-fg"
				>
					Retry
				</button>
			</div>
		);
	}

	if (file.binary) return <BinaryPreview file={file} />;

	const previewKind = textPreviewKind(file.path);
	const dataFormat =
		previewKind === "json" ||
		previewKind === "yaml" ||
		previewKind === "toml" ||
		previewKind === "xml" ||
		previewKind === "csv" ||
		previewKind === "tsv"
			? previewKind
			: null;
	const previewSrc = workspaceFilePreviewURL(file.path);
	return (
		<div className="flex h-full min-h-0 flex-col">
			{document.conflict && (
				<div className="flex shrink-0 items-center gap-2 border-b border-warning/30 bg-warning/5 px-3 py-2 text-[11px] text-warning">
					<AlertTriangle size={13} className="shrink-0" />
					<span className="min-w-0 flex-1">
						This file changed on disk while you had unsaved edits.
					</span>
					<button
						type="button"
						onClick={onReload}
						className="rounded px-2 py-1 hover:bg-warning/10"
					>
						Reload from disk
					</button>
				</div>
			)}
			{document.saveError && (
				<div className="shrink-0 border-b border-danger/30 bg-danger/5 px-3 py-2 text-[11px] text-danger">
					{document.saveError}
				</div>
			)}
			<div className="min-h-0 flex-1">
				{previewKind === "html" && view === "preview" ? (
					<iframe
						key={document.revision}
						src={previewSrc}
						title={`Preview of ${file.path}`}
						sandbox="allow-scripts allow-same-origin"
						referrerPolicy="no-referrer"
						className="h-full w-full border-0 bg-bg"
						style={{ colorScheme: scheme }}
					/>
				) : previewKind === "svg" && view === "preview" ? (
					<ImagePreview src={previewSrc} name={file.path} />
				) : previewKind === "markdown" && view === "preview" ? (
					<div className="h-full overflow-auto bg-bg">
						<article
							data-markdown-document
							aria-label={`Preview of ${file.path}`}
							className="mx-auto max-w-4xl px-8 py-7 text-[13px] leading-relaxed select-text"
						>
							<MarkdownContent text={document.draft} />
						</article>
					</div>
				) : previewKind === "mermaid" && view === "preview" ? (
					<MermaidPreview text={document.draft} path={file.path} />
				) : dataFormat && view === "preview" ? (
					<DataPreview
						text={document.draft}
						format={dataFormat}
						path={file.path}
					/>
				) : (
					<Editor
						height="100%"
						path={`/${file.path}`}
						language={file.language || undefined}
						defaultValue={document.draft}
						theme={wingmanThemeName(scheme)}
						beforeMount={defineWingmanThemes}
						onMount={(editor, monaco) => {
							disposeEditorIntegration();
							setContextMenu(null);
							editorRef.current = editor;
							monacoRef.current = monaco;
							syncingEditorRef.current = true;
							try {
								synchronizeEditorDraft(editor, document.draft);
							} finally {
								syncingEditorRef.current = false;
							}
							transformBridgeRef.current = createMonacoTransformBridge(
								monaco,
								editor,
							);
							refreshTabIntegration();
							// Wingman owns the command surface; keep Monaco's standalone
							// palette shortcuts from opening a second command UI.
							editor.addCommand(monaco.KeyCode.F1, () => {});
							editor.addCommand(
								monaco.KeyMod.CtrlCmd |
									monaco.KeyMod.Shift |
									monaco.KeyCode.KeyP,
								() => {},
							);
							contextMenuListenerRef.current = editor.onContextMenu((event) => {
								const position = event.target.position;
								if (!position) return;
								event.event.preventDefault();
								event.event.stopPropagation();
								const selection = editor.getSelection();
								if (!selection?.containsPosition(position)) {
									editor.setPosition(position);
								}
								editor.focus();
								setContextMenu({
									x: event.event.posx,
									y: event.event.posy,
									altKey: event.event.altKey,
									editor,
								});
							});
							editor.addCommand(
								monaco.KeyMod.Shift | monaco.KeyCode.F10,
								() => {
									editor.focus();
									const position = editor.getPosition();
									const visible =
										position && editor.getScrolledVisiblePosition(position);
									const bounds = editor.getDomNode()?.getBoundingClientRect();
									if (!bounds) return;
									setContextMenu({
										x: bounds.left + (visible?.left ?? 8),
										y:
											bounds.top +
											(visible?.top ?? 8) +
											(visible?.height ?? 16),
										altKey: false,
										editor,
									});
								},
							);
							if (!document.external) {
								debugBridgeRef.current = createMonacoDebugBridge({
									monaco,
									editor,
									path: file.path,
									onLaunchTarget: (target, action) => {
										void (async () => {
											const saved = await onSaveRef.current();
											if (saved.ok) {
												onLaunchDebugRef.current?.(target, action);
											}
										})();
									},
								});
								refreshLSPIntegration();
							}
							revealEditorPosition(editor, line, column);
						}}
						onChange={(value) => {
							if (!syncingEditorRef.current) onChange(value ?? "");
						}}
						options={{
							contextmenu: false,
							codeLens: true,
							find: { addExtraSpaceOnTop: false },
							glyphMargin: true,
							minimap: { enabled: false },
							fontSize: 12,
							lineNumbers: "on",
							scrollBeyondLastLine: false,
							wordWrap: "on",
							renderWhitespace: "none",
							padding: { top: 8 },
							suggestOnTriggerCharacters: true,
							parameterHints: { enabled: true },
							readOnly: document.external,
						}}
					/>
				)}
			</div>
			{view === "code" && contextMenu && (
				<EditorContextMenu
					editor={contextMenu.editor}
					openAt={contextMenu}
					readOnly={document.external}
					initialAltKey={contextMenu.altKey}
					supportsLanguageFeature={(feature) =>
						lspBridgeRef.current?.supports(feature) ?? false
					}
					onTransformSelection={() => openTransformPrompt(contextMenu.editor)}
					onAskSelection={() => askAboutSelection(contextMenu.editor)}
					onClose={() => setContextMenu(null)}
				/>
			)}
			{view === "code" && transformTarget && (
				<InlineTransformPrompt
					reference={transformTarget.reference}
					selectionLabel={transformTarget.label}
					onTransform={async (instruction, signal) => {
						const editor = editorRef.current;
						const model = editor?.getModel();
						if (!editor || !model || !file) {
							throw new Error("The editor is no longer available.");
						}
						if (model.getVersionId() !== transformTarget.version) {
							throw new Error(
								"The selection changed. Select it again and retry.",
							);
						}
						const result = await transformEditorSelection(
							{
								path: file.path,
								content: model.getValue(),
								range: transformTarget.range,
								instruction,
								version: transformTarget.version,
							},
							signal,
						);
						if (!isEditorTransformResponse(result)) {
							throw new Error("Wingman returned an invalid transformation.");
						}
						if (result.version !== transformTarget.version) {
							throw new Error("The transformation response is stale.");
						}
						if (!result.edit) {
							throw new Error("Wingman did not find a useful change.");
						}
						if (
							!transformBridgeRef.current?.preview(result.edit, result.version)
						) {
							throw new Error(
								"The selection changed before the preview opened.",
							);
						}
					}}
					onAskWingman={() => askWithSelectionTarget(transformTarget)}
					onClose={() => setTransformTarget(null)}
				/>
			)}
		</div>
	);
});

function BinaryPreview({
	file,
}: {
	file: import("../types/protocol").FileContent;
}) {
	const mime = file.mime ?? "application/octet-stream";
	const src = workspaceFileDownloadURL(file.path);
	return (
		<div className="h-full w-full overflow-auto bg-bg">
			<PreviewBody mime={mime} src={src} name={file.path} />
		</div>
	);
}

function PreviewBody({
	mime,
	src,
	name,
}: {
	mime: string;
	src: string;
	name: string;
}) {
	if (mime.startsWith("image/")) {
		return <ImagePreview src={src} name={name} />;
	}
	if (mime.startsWith("video/")) {
		return (
			<div className="flex h-full w-full items-center justify-center bg-black/40 p-6">
				<video src={src} controls className="max-h-full max-w-full">
					<track kind="captions" />
				</video>
			</div>
		);
	}
	if (mime.startsWith("audio/")) {
		return (
			<div className="flex h-full w-full items-center justify-center p-6">
				<audio src={src} controls className="w-full max-w-xl">
					<track kind="captions" />
				</audio>
			</div>
		);
	}
	if (mime === "application/pdf") {
		return (
			<object
				data={src}
				type="application/pdf"
				className="h-full w-full"
				aria-label="PDF preview"
			>
				<UnknownBinary />
			</object>
		);
	}
	return <UnknownBinary />;
}

function ImagePreview({ src, name }: { src: string; name: string }) {
	return (
		<div
			data-image-preview
			className="flex h-full min-h-0 w-full items-center justify-center overflow-auto p-6"
		>
			<img
				src={src}
				alt={name}
				className="h-auto w-auto object-contain"
				style={{
					maxWidth: "min(100%, 1024px)",
					maxHeight: "min(100%, 800px)",
				}}
			/>
		</div>
	);
}

function UnknownBinary() {
	return (
		<div className="flex h-full w-full flex-col items-center justify-center gap-3 p-6 text-center">
			<FileDigit size={36} className="text-fg-dim/60" strokeWidth={1.25} />
			<div className="font-mono text-[12px] text-fg-dim">
				Binary file — no inline preview
			</div>
		</div>
	);
}
