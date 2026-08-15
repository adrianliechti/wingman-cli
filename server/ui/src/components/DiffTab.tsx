import { DiffEditor } from "@monaco-editor/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type { DiffEntry, DiffLayer, ServerMessage } from "../types/protocol";

interface Props {
	path: string;
	layer?: DiffLayer;
	sessionId: string;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
	onDeleted?: () => void;
}

export function DiffTab({
	path,
	layer,
	sessionId,
	subscribe,
	onDeleted,
}: Props) {
	const [diff, setDiff] = useState<DiffEntry | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string | null>(null);
	const scheme = useColorScheme();

	const onDeletedRef = useRef(onDeleted);
	useEffect(() => {
		onDeletedRef.current = onDeleted;
	});
	const hadDiffRef = useRef(false);

	const load = useCallback(async () => {
		try {
			const params = new URLSearchParams({ path });
			if (layer) params.set("layer", layer);
			if (sessionId) params.set("session", sessionId);
			const res = await fetch(`/api/diffs?${params.toString()}`);
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
	}, [path, layer, sessionId]);

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
				Loading…
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

	const original = diff.original ?? "";
	const modified = diff.modified ?? "";
	if (original !== "" || modified !== "") {
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
					find: { addExtraSpaceOnTop: false },
					renderSideBySide: !inline,
					renderOverviewRuler: false,
					overviewRulerLanes: 0,
					overviewRulerBorder: false,
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

interface DiffRow {
	cls: string;
	oldNum: string | number;
	newNum: string | number;
	line: string;
}

function buildDiffRows(patch: string): DiffRow[] {
	const lines = patch.split("\n");
	const rows: DiffRow[] = [];
	let oldLine = 0;
	let newLine = 0;
	for (let i = 0; i < lines.length; i++) {
		const line = lines[i];
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

		rows.push({ cls, oldNum, newNum, line });
	}
	return rows;
}

export function DiffView({ patch }: { patch: string }) {
	const rows = buildDiffRows(patch);
	return (
		<div className="font-mono text-[12px] leading-[1.55] py-2">
			{rows.map((row, i) => (
				<div key={i} className={`flex ${row.cls}`}>
					<span className="w-10 shrink-0 text-right pr-1 text-fg-dim select-none">
						{row.oldNum}
					</span>
					<span className="w-10 shrink-0 text-right pr-2 text-fg-dim select-none">
						{row.newNum}
					</span>
					<span className="flex-1 whitespace-pre px-2 break-all">
						{row.line || "\u00A0"}
					</span>
				</div>
			))}
		</div>
	);
}
