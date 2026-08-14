import Editor, { type OnMount } from "@monaco-editor/react";
import { AlertTriangle, FileDigit, Loader2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import type { OpenDocument, SaveResult } from "../hooks/useOpenDocuments";
import {
	createMonacoLSPBridge,
	type MonacoLSPBridge,
	revealEditorPosition,
} from "../monacoLsp";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type { ServerMessage } from "../types/protocol";
import type { WorkspaceEditEnvelope } from "../workspaceEdit";
import { textPreviewKind } from "../utils/filePreview";
import { DataPreview } from "./DataPreview";
import { EditorContextMenu } from "./EditorContextMenu";
import { MarkdownContent } from "./MarkdownContent";
import { MermaidPreview } from "./MermaidPreview";

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
	view?: "code" | "preview";
}

export function FileTab({
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
	view = "code",
}: Props) {
	const editorRef = useRef<Parameters<OnMount>[0] | null>(null);
	const contextMenuListenerRef = useRef<{ dispose(): void } | null>(null);
	const lspBridgeRef = useRef<MonacoLSPBridge | null>(null);
	const diagnosticsTimerRef = useRef<number | null>(null);
	const onOpenFileRef = useRef(onOpenFile);
	const onApplyWorkspaceEditRef = useRef(onApplyWorkspaceEdit);
	const onSaveRef = useRef(onSave);
	const scheme = useColorScheme();
	const [contextMenu, setContextMenu] = useState<{
		x: number;
		y: number;
		altKey: boolean;
	} | null>(null);
	const [, setLanguageFeaturesRevision] = useState(0);
	onOpenFileRef.current = onOpenFile;
	onApplyWorkspaceEditRef.current = onApplyWorkspaceEdit;
	onSaveRef.current = onSave;

	const dirty = document.draft !== document.savedContent;
	const file = document.file;

	useEffect(() => {
		const editor = editorRef.current;
		if (!editor || !line || line < 1) return;
		revealEditorPosition(editor, line, column);
	}, [line, column, navigationKey]);

	useEffect(() => {
		return () => {
			contextMenuListenerRef.current?.dispose();
			contextMenuListenerRef.current = null;
			if (diagnosticsTimerRef.current !== null) {
				window.clearTimeout(diagnosticsTimerRef.current);
			}
			lspBridgeRef.current?.dispose();
			lspBridgeRef.current = null;
			editorRef.current = null;
		};
	}, []);

	const loadDiagnostics = useCallback(async () => {
		await lspBridgeRef.current?.refreshDiagnostics();
	}, []);

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
	const previewSrc = `/api/files/preview?path=${encodeURIComponent(file.path)}`;
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
						value={document.draft}
						theme={wingmanThemeName(scheme)}
						beforeMount={defineWingmanThemes}
						onMount={(editor, monaco) => {
							editorRef.current = editor;
							// Wingman owns the command surface; keep Monaco's standalone
							// palette shortcuts from opening a second command UI.
							editor.addCommand(monaco.KeyCode.F1, () => {});
							editor.addCommand(
								monaco.KeyMod.CtrlCmd |
									monaco.KeyMod.Shift |
									monaco.KeyCode.KeyP,
								() => {},
							);
							contextMenuListenerRef.current?.dispose();
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
									});
								},
							);
							lspBridgeRef.current?.dispose();
							if (!document.external) {
								lspBridgeRef.current = createMonacoLSPBridge({
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
								});
								void loadDiagnostics();
								editor.addCommand(
									monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
									() => void onSaveRef.current(),
								);
							}
							revealEditorPosition(editor, line, column);
						}}
						onChange={(value) => onChange(value ?? "")}
						options={{
							contextmenu: false,
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
			{contextMenu && editorRef.current && (
				<EditorContextMenu
					editor={editorRef.current}
					openAt={contextMenu}
					readOnly={document.external}
					initialAltKey={contextMenu.altKey}
					supportsLanguageFeature={(feature) =>
						lspBridgeRef.current?.supports(feature) ?? false
					}
					onClose={() => setContextMenu(null)}
				/>
			)}
		</div>
	);
}

function BinaryPreview({
	file,
}: {
	file: import("../types/protocol").FileContent;
}) {
	const mime = file.mime ?? "application/octet-stream";
	const src = `/api/files/download?path=${encodeURIComponent(file.path)}`;
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
