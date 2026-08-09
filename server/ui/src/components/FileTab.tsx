import Editor, { type Monaco } from "@monaco-editor/react";
import { FileDigit } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type { FileContent, ServerMessage } from "../types/protocol";

interface Props {
	path: string;
	line?: number;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
	onDeleted?: () => void;
	onDirtyChange?: (dirty: boolean) => void;
	view?: "code" | "preview";
}

export function FileTab({
	path,
	line,
	subscribe,
	onDeleted,
	onDirtyChange,
	view = "code",
}: Props) {
	const [file, setFile] = useState<FileContent | null>(null);
	const [loading, setLoading] = useState(true);
	const [value, setValue] = useState("");
	const [saving, setSaving] = useState(false);
	const [previewRevision, setPreviewRevision] = useState(0);
	const monacoRef = useRef<Monaco | null>(null);
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

	const load = useCallback(async () => {
		try {
			const res = await fetch(
				`/api/files/read?path=${encodeURIComponent(path)}`,
			);
			if (res.status === 404) {
				onDeletedRef.current?.();
				return;
			}
			if (!res.ok) {
				setLoading(false);
				return;
			}
			const data: FileContent = await res.json();
			setFile(data);
			setValue(data.content ?? "");
			setLoading(false);
		} catch {
			setLoading(false);
		}
	}, [path]);

	useEffect(() => {
		setLoading(true);
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "files_changed") {
				setPreviewRevision((revision) => revision + 1);
				if (dirtyRef.current) return;
				load();
			}
		});
	}, [subscribe, load]);

	const save = useCallback(async () => {
		const f = fileRef.current;
		if (!f || f.binary || saving) return;
		const content = valueRef.current;
		if (content === (f.content ?? "")) return;
		setSaving(true);
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
			setSaving(false);
		}
	}, [saving]);

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
					className="h-full w-full border-0 bg-white"
				/>
			) : (
				<Editor
					height="100%"
					language={file.language || undefined}
					value={value}
					theme={wingmanThemeName(scheme)}
					beforeMount={(monaco) => {
						monacoRef.current = monaco;
						defineWingmanThemes(monaco);
					}}
					onMount={(editor, monaco) => {
						if (line && line > 0) {
							editor.revealLineInCenter(line);
							editor.setPosition({ lineNumber: line, column: 1 });
						}
						editor.addCommand(
							monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
							() => {
								void save();
							},
						);
					}}
					onChange={(v) => setValue(v ?? "")}
					options={{
						minimap: { enabled: false },
						fontSize: 12,
						lineNumbers: "on",
						scrollBeyondLastLine: false,
						wordWrap: "on",
						renderWhitespace: "none",
						padding: { top: 8 },
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
