import Editor, { type Monaco } from "@monaco-editor/react";
import { useEffect, useRef, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type { FileContent } from "../types/protocol";

interface Props {
	path: string;
	line?: number;
}

export function FileTab({ path, line }: Props) {
	const [file, setFile] = useState<FileContent | null>(null);
	const [loading, setLoading] = useState(true);
	const monacoRef = useRef<Monaco | null>(null);
	const scheme = useColorScheme();

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
