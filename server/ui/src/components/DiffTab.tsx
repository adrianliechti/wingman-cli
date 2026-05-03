import { DiffEditor } from "@monaco-editor/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type { DiffEntry, ServerMessage } from "../types/protocol";

interface Props {
	path: string;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
	onDeleted?: () => void;
}

export function DiffTab({ path, subscribe, onDeleted }: Props) {
	const [diff, setDiff] = useState<DiffEntry | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string | null>(null);
	const scheme = useColorScheme();

	// Ref so `load` stays stable across renders and the WebSocket subscription
	// doesn't tear down/re-subscribe each time.
	const onDeletedRef = useRef(onDeleted);
	onDeletedRef.current = onDeleted;
	// Track whether we've ever found a diff for this path. Closing the tab on a
	// missing entry only makes sense after we've shown one — opening directly
	// from a stale link should still render the "No changes" state.
	const hadDiffRef = useRef(false);

	const load = useCallback(async () => {
		try {
			const res = await fetch("/api/diffs");
			if (!res.ok) {
				setError("failed to load diffs");
				setLoading(false);
				return;
			}
			const data: DiffEntry[] = await res.json();
			const match = data.find((d) => d.path === path) || null;
			if (!match && hadDiffRef.current) {
				onDeletedRef.current?.();
				return;
			}
			if (match) hadDiffRef.current = true;
			setDiff(match);
			setError(null);
			setLoading(false);
		} catch (e) {
			setError(String(e));
			setLoading(false);
		}
	}, [path]);

	useEffect(() => {
		hadDiffRef.current = false;
		setLoading(true);
		setError(null);
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "diffs_changed") {
				load();
			}
		});
	}, [subscribe, load]);

	if (loading) {
		return (
			<div className="h-full flex items-center justify-center text-fg-dim text-[12px]">
				Loading diff…
			</div>
		);
	}
	if (error) {
		return (
			<div className="h-full flex items-center justify-center text-danger text-[12px]">
				{error}
			</div>
		);
	}
	if (!diff) {
		return (
			<div className="h-full flex items-center justify-center text-fg-dim text-[12px]">
				No changes for {path}
			</div>
		);
	}

	// Prefer Monaco's diff editor whenever at least one side has content.
	// The backend strips empty strings from JSON, so for an added file
	// `original` is undefined (not "") and for a deleted file `modified` is
	// undefined; treat undefined as empty so those still hit DiffEditor.
	const original = diff.original ?? "";
	const modified = diff.modified ?? "";
	if (original !== "" || modified !== "") {
		// Side-by-side only makes sense for modified files. For added or deleted
		// files inline mode renders a single column with green/red line backgrounds,
		// which avoids a giant empty pane on one side.
		const inline = diff.status === "added" || diff.status === "deleted";
		return (
			<DiffEditor
				height="100%"
				language={diff.language || undefined}
				original={original}
				modified={modified}
				theme={wingmanThemeName(scheme)}
				beforeMount={defineWingmanThemes}
				options={{
					readOnly: true,
					renderSideBySide: !inline,
					minimap: { enabled: false },
					fontSize: 12,
					lineNumbers: "on",
					scrollBeyondLastLine: false,
					renderWhitespace: "none",
					padding: { top: 8 },
					hideUnchangedRegions: { enabled: !inline },
				}}
			/>
		);
	}

	return (
		<div className="h-full overflow-auto bg-bg">
			<DiffView patch={diff.patch} />
		</div>
	);
}

function DiffView({ patch }: { patch: string }) {
	const lines = patch.split("\n");
	let oldLine = 0;
	let newLine = 0;

	return (
		<div className="font-mono text-[12px] leading-[1.55] py-2">
			{lines.map((line, i) => {
				let cls = "text-fg-muted";
				let oldNum: string | number = "";
				let newNum: string | number = "";

				if (line.startsWith("@@")) {
					const m = /@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
					if (m) {
						oldLine = parseInt(m[1], 10);
						newLine = parseInt(m[2], 10);
					}
					cls = "text-purple bg-[rgba(197,134,192,0.08)]";
				} else if (
					line.startsWith("+++") ||
					line.startsWith("---") ||
					line.startsWith("diff ") ||
					line.startsWith("index ")
				) {
					cls = "text-fg-dim";
				} else if (line.startsWith("+")) {
					cls = "text-success bg-[rgba(78,201,176,0.1)]";
					newNum = newLine++;
				} else if (line.startsWith("-")) {
					cls = "text-danger bg-[rgba(244,135,113,0.1)]";
					oldNum = oldLine++;
				} else if (line.length > 0 || i < lines.length - 1) {
					oldNum = oldLine++;
					newNum = newLine++;
				}

				return (
					<div key={i} className={`flex ${cls}`}>
						<span className="w-10 shrink-0 text-right pr-1 text-fg-dim select-none">
							{oldNum}
						</span>
						<span className="w-10 shrink-0 text-right pr-2 text-fg-dim select-none">
							{newNum}
						</span>
						<span className="flex-1 whitespace-pre px-2 break-all">
							{line || "\u00A0"}
						</span>
					</div>
				);
			})}
		</div>
	);
}
