import Editor, { type OnMount } from "@monaco-editor/react";
import { FileDigit } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import {
	createMonacoLSPBridge,
	type MonacoLSPBridge,
	revealEditorPosition,
} from "../monacoLsp";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type { FileContent, ServerMessage } from "../types/protocol";

interface Props {
	path: string;
	line?: number;
	column?: number;
	navigationKey?: number;
	external?: boolean;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
	onDeleted?: () => void;
	onDirtyChange?: (dirty: boolean) => void;
	onOpenFile?: (
		path: string,
		line: number,
		column: number,
		external?: boolean,
	) => void;
	view?: "code" | "preview";
}

export function FileTab({
	path,
	line,
	column,
	navigationKey,
	external = false,
	subscribe,
	onDeleted,
	onDirtyChange,
	onOpenFile,
	view = "code",
}: Props) {
	const [file, setFile] = useState<FileContent | null>(null);
	const [loading, setLoading] = useState(true);
	const [value, setValue] = useState("");
	const [previewRevision, setPreviewRevision] = useState(0);
	const editorRef = useRef<Parameters<OnMount>[0] | null>(null);
	const lspBridgeRef = useRef<MonacoLSPBridge | null>(null);
	const loadControllerRef = useRef<AbortController | null>(null);
	const diagnosticsEventTimerRef = useRef<number | null>(null);
	const savingRef = useRef(false);
	const scheme = useColorScheme();

	const onDeletedRef = useRef(onDeleted);
	useEffect(() => {
		onDeletedRef.current = onDeleted;
	});

	const dirty = file !== null && !file.binary && value !== (file.content ?? "");
	const dirtyRef = useRef(dirty);
	const valueRef = useRef(value);
	const fileRef = useRef(file);
	useEffect(() => {
		dirtyRef.current = dirty;
		valueRef.current = value;
		fileRef.current = file;
	});

	const onDirtyChangeRef = useRef(onDirtyChange);
	useEffect(() => {
		onDirtyChangeRef.current = onDirtyChange;
	});

	useEffect(() => {
		onDirtyChangeRef.current?.(dirty);
	}, [dirty]);

	useEffect(() => {
		return () => onDirtyChangeRef.current?.(false);
	}, []);

	const onOpenFileRef = useRef(onOpenFile);
	useEffect(() => {
		onOpenFileRef.current = onOpenFile;
	});

	useEffect(() => {
		const editor = editorRef.current;
		if (!editor || !line || line < 1) return;
		revealEditorPosition(editor, line, column);
	}, [line, column, navigationKey]);

	useEffect(() => {
		return () => {
			loadControllerRef.current?.abort();
			if (diagnosticsEventTimerRef.current !== null) {
				window.clearTimeout(diagnosticsEventTimerRef.current);
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
	}, [file, value, dirty, loadDiagnostics]);

	const load = useCallback(async () => {
		loadControllerRef.current?.abort();
		const controller = new AbortController();
		loadControllerRef.current = controller;
		try {
			const res = await fetch(
				`${external ? "/api/lsp/file" : "/api/files/read"}?path=${encodeURIComponent(path)}`,
				{ signal: controller.signal },
			);
			if (controller.signal.aborted) return;
			if (res.status === 404) {
				onDeletedRef.current?.();
				return;
			}
			if (!res.ok) {
				setLoading(false);
				return;
			}
			const data: FileContent = await res.json();
			if (fileRef.current && dirtyRef.current) return;
			setFile(data);
			setValue(data.content ?? "");
			setLoading(false);
		} catch (error) {
			if (
				loadControllerRef.current === controller &&
				!(error instanceof DOMException && error.name === "AbortError")
			) {
				setLoading(false);
			}
		} finally {
			if (loadControllerRef.current === controller) {
				loadControllerRef.current = null;
			}
		}
	}, [path, external]);

	useEffect(() => {
		setLoading(true);
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		const unsubscribe = subscribe((msg) => {
			if (msg.type === "files_changed") {
				setPreviewRevision((revision) => revision + 1);
				if (dirtyRef.current) return;
				load();
			} else if (msg.type === "diagnostics_changed") {
				if (diagnosticsEventTimerRef.current !== null) {
					window.clearTimeout(diagnosticsEventTimerRef.current);
				}
				diagnosticsEventTimerRef.current = window.setTimeout(() => {
					diagnosticsEventTimerRef.current = null;
					void loadDiagnostics();
				}, 200);
			}
		});
		return () => {
			unsubscribe();
			if (diagnosticsEventTimerRef.current !== null) {
				window.clearTimeout(diagnosticsEventTimerRef.current);
				diagnosticsEventTimerRef.current = null;
			}
		};
	}, [subscribe, load, loadDiagnostics]);

	const save = useCallback(async () => {
		const f = fileRef.current;
		if (!f || f.binary || savingRef.current) return;
		const content = valueRef.current;
		if (content === (f.content ?? "")) return;
		savingRef.current = true;
		try {
			const res = await fetch("/api/files/write", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ path: f.path, content }),
			});
			if (res.ok) {
				setFile({ ...f, content });
			}
		} catch {
		} finally {
			savingRef.current = false;
		}
	}, []);

	if (loading) {
		return (
			<div className="flex items-center justify-center h-full text-fg-dim text-[12px]">
				Loading…
			</div>
		);
	}

	if (!file) {
		return (
			<div className="flex items-center justify-center h-full text-fg-dim text-[12px]">
				Failed to load file
			</div>
		);
	}

	if (file.binary) {
		return <BinaryPreview file={file} />;
	}

	const isHtml = file.language === "html" || /\.html?$/i.test(file.path);
	const previewSrc = `/api/files/preview?path=${encodeURIComponent(file.path)}`;
	return (
		<div className="h-full min-h-0">
			{isHtml && view === "preview" ? (
				<iframe
					key={previewRevision}
					src={previewSrc}
					title={`Preview of ${file.path}`}
					sandbox="allow-scripts allow-same-origin"
					referrerPolicy="no-referrer"
					className="h-full w-full border-0 bg-bg"
					style={{ colorScheme: scheme }}
				/>
			) : (
				<Editor
					height="100%"
					path={`/${file.path}`}
					language={file.language || undefined}
					value={value}
					theme={wingmanThemeName(scheme)}
					beforeMount={(monaco) => {
						defineWingmanThemes(monaco);
					}}
					onMount={(editor, monaco) => {
						editorRef.current = editor;
						lspBridgeRef.current?.dispose();
						if (!external) {
							lspBridgeRef.current = createMonacoLSPBridge({
								monaco,
								editor,
								file,
								getDirtyContent: () =>
									dirtyRef.current ? valueRef.current : undefined,
								onOpenFile: (
									targetPath,
									targetLine,
									targetColumn,
									targetExternal,
								) =>
									onOpenFileRef.current?.(
										targetPath,
										targetLine,
										targetColumn,
										targetExternal,
									),
							});
							void loadDiagnostics();
							editor.addCommand(
								monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
								() => {
									void save();
								},
							);
						}
						revealEditorPosition(editor, line, column);
					}}
					onChange={(v) => {
						loadControllerRef.current?.abort();
						const nextValue = v ?? "";
						valueRef.current = nextValue;
						const currentFile = fileRef.current;
						dirtyRef.current =
							currentFile !== null &&
							!currentFile.binary &&
							nextValue !== (currentFile.content ?? "");
						setValue(nextValue);
					}}
					options={{
						minimap: { enabled: false },
						fontSize: 12,
						lineNumbers: "on",
						scrollBeyondLastLine: false,
						wordWrap: "on",
						renderWhitespace: "none",
						padding: { top: 8 },
						readOnly: external,
					}}
				/>
			)}
		</div>
	);
}

function BinaryPreview({ file }: { file: FileContent }) {
	const mime = file.mime ?? "application/octet-stream";
	const src = `/api/files/download?path=${encodeURIComponent(file.path)}`;

	return (
		<div className="h-full w-full bg-bg overflow-auto">
			<PreviewBody mime={mime} src={src} />
		</div>
	);
}

function PreviewBody({ mime, src }: { mime: string; src: string }) {
	if (mime.startsWith("image/")) {
		return (
			<div className="h-full w-full flex items-center justify-center p-6">
				<img
					src={src}
					alt=""
					className="max-h-full max-w-full object-contain [image-rendering:auto]"
				/>
			</div>
		);
	}
	if (mime.startsWith("video/")) {
		return (
			<div className="h-full w-full flex items-center justify-center p-6 bg-black/40">
				<video src={src} controls className="max-h-full max-w-full">
					<track kind="captions" />
				</video>
			</div>
		);
	}
	if (mime.startsWith("audio/")) {
		return (
			<div className="h-full w-full flex items-center justify-center p-6">
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
				className="w-full h-full"
				aria-label="PDF preview"
			>
				<UnknownBinary />
			</object>
		);
	}
	return <UnknownBinary />;
}

function UnknownBinary() {
	return (
		<div className="h-full w-full flex flex-col items-center justify-center gap-3 p-6 text-center">
			<FileDigit size={36} className="text-fg-dim/60" strokeWidth={1.25} />
			<div className="text-fg-dim text-[12px] font-mono">
				Binary file — no inline preview
			</div>
		</div>
	);
}
