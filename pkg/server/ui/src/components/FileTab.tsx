import Editor, { type Monaco } from "@monaco-editor/react";
import { useEffect, useRef, useState } from "react";
import type { FileContent } from "../types/protocol";

interface Props {
	path: string;
	line?: number;
}

const themeRegistered = { current: false };

function defineTheme(monaco: Monaco) {
	if (themeRegistered.current) return;
	themeRegistered.current = true;

	monaco.editor.defineTheme("wingman", {
		base: "vs-dark",
		inherit: true,
		rules: [
			{ token: "comment", foreground: "555555" },
			{ token: "keyword", foreground: "a78bfa" },
			{ token: "string", foreground: "34d399" },
			{ token: "number", foreground: "fbbf24" },
			{ token: "type", foreground: "60a5fa" },
		],
		colors: {
			"editor.background": "#0a0a0a",
			"editor.foreground": "#e0e0e0",
			"editor.lineHighlightBackground": "#111111",
			"editor.selectionBackground": "#ffffff26",
			"editor.inactiveSelectionBackground": "#ffffff15",
			"editorLineNumber.foreground": "#333333",
			"editorLineNumber.activeForeground": "#666666",
			"editorCursor.foreground": "#888888",
			"editor.lineHighlightBorder": "#00000000",
			"editorWidget.background": "#111111",
			"editorWidget.border": "#1e1e1e",
			"editorGutter.background": "#0a0a0a",
			"scrollbarSlider.background": "#ffffff14",
			"scrollbarSlider.hoverBackground": "#ffffff26",
			"scrollbarSlider.activeBackground": "#ffffff33",
		},
	});
}

export function FileTab({ path, line }: Props) {
	const [file, setFile] = useState<FileContent | null>(null);
	const [loading, setLoading] = useState(true);
	const monacoRef = useRef<Monaco | null>(null);

	useEffect(() => {
		setLoading(true);
		fetch(`/api/files/read?path=${encodeURIComponent(path)}`)
			.then((r) => r.json())
			.then((data: FileContent) => {
				setFile(data);
				setLoading(false);
			})
			.catch(() => setLoading(false));
	}, [path]);

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
			theme="wingman"
			beforeMount={(monaco) => {
				monacoRef.current = monaco;
				defineTheme(monaco);
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
