import { useEffect, useState } from "react";
import type { DiffEntry } from "../types/protocol";

interface Props {
	path: string;
}

export function DiffTab({ path }: Props) {
	const [diff, setDiff] = useState<DiffEntry | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string | null>(null);

	useEffect(() => {
		let cancelled = false;
		setLoading(true);
		setError(null);
		fetch("/api/diffs")
			.then((r) =>
				r.ok ? r.json() : Promise.reject(new Error("failed to load diffs")),
			)
			.then((data: DiffEntry[]) => {
				if (cancelled) return;
				const match = data.find((d) => d.path === path) || null;
				setDiff(match);
				setLoading(false);
			})
			.catch((e) => {
				if (cancelled) return;
				setError(String(e));
				setLoading(false);
			});
		return () => {
			cancelled = true;
		};
	}, [path]);

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

	return (
		<div className="h-full overflow-auto bg-bg">
			<DiffView patch={diff.patch} />
		</div>
	);
}

export function DiffView({ patch }: { patch: string }) {
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
