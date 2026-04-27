import Editor, { type Monaco } from "@monaco-editor/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type { FileContent, ServerMessage } from "../types/protocol";

interface Props {
	path: string;
	line?: number;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
	onDeleted?: () => void;
}

export function FileTab({ path, line, subscribe, onDeleted }: Props) {
	const [file, setFile] = useState<FileContent | null>(null);
	const [loading, setLoading] = useState(true);
	const monacoRef = useRef<Monaco | null>(null);
	const scheme = useColorScheme();

	// Keep onDeleted in a ref so `load` stays stable and the WebSocket
	// subscription doesn't tear down/re-subscribe on every render.
	const onDeletedRef = useRef(onDeleted);
	useEffect(() => {
		onDeletedRef.current = onDeleted;
	});

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
			setLoading(false);
		} catch {
			setLoading(false);
		}
	}, [path]);

	useEffect(() => {
		// eslint-disable-next-line react-hooks/set-state-in-effect -- standard data-load on path change
		setLoading(true);
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "files_changed") {
				load();
			}
		});
	}, [subscribe, load]);

	if (loading) {
		return (
			<div className="flex items-center justify-center h-full text-fg-dim text-sm">
				Loading...
			</div>
		);
	}

	if (!file) {
		return (
			<div className="flex items-center justify-center h-full text-fg-dim text-sm">
				Failed to load file
			</div>
		);
	}

	return (
		<Editor
			height="100%"
			language={file.language || undefined}
			value={file.content}
			theme={wingmanThemeName(scheme)}
			beforeMount={(monaco) => {
				monacoRef.current = monaco;
				defineWingmanThemes(monaco);
			}}
			onMount={(editor) => {
				if (line && line > 0) {
					editor.revealLineInCenter(line);
					editor.setPosition({ lineNumber: line, column: 1 });
				}
			}}
			options={{
				readOnly: true,
				minimap: { enabled: false },
				fontSize: 12,
				lineNumbers: "on",
				scrollBeyondLastLine: false,
				wordWrap: "on",
				renderWhitespace: "none",
				padding: { top: 8 },
			}}
		/>
	);
}
