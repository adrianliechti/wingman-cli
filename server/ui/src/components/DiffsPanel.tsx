import { useCallback, useEffect, useState } from "react";
import type { DiffEntry, ServerMessage } from "../types/protocol";

interface Props {
	onOpenDiff?: (path: string) => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function DiffsPanel({ onOpenDiff, subscribe }: Props) {
	const [diffs, setDiffs] = useState<DiffEntry[]>([]);

	const loadDiffs = useCallback(async () => {
		try {
			const res = await fetch("/api/diffs");
			if (!res.ok) {
				setDiffs([]);
				return;
			}
			const data: DiffEntry[] = await res.json();
			setDiffs(data);
		} catch {
			setDiffs([]);
		}
	}, []);

	useEffect(() => {
		loadDiffs();
	}, [loadDiffs]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "diffs_changed") {
				loadDiffs();
			}
		});
	}, [subscribe, loadDiffs]);

	const statusLabels: Record<string, string> = {
		added: "A",
		modified: "M",
		deleted: "D",
	};
	const statusColors: Record<string, string> = {
		added: "text-success",
		modified: "text-warning",
		deleted: "text-danger",
	};

	return (
		<div className="flex flex-col h-full overflow-hidden bg-bg">
			<div className="overflow-y-auto flex-1 py-2">
				{diffs.length === 0 && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						No changes yet
					</div>
				)}
				{diffs.map((diff) => {
					const fileName = diff.path.split("/").pop() || diff.path;
					const dir = diff.path
						.slice(0, diff.path.length - fileName.length)
						.replace(/\/$/, "");
					return (
						<div
							key={diff.path}
							className="group flex items-center gap-2 px-3 py-1 cursor-pointer text-[12px] text-fg-muted hover:bg-bg-hover hover:text-fg transition-colors"
							onClick={() => onOpenDiff?.(diff.path)}
							title={diff.path}
						>
							<span
								className={`text-[10.5px] font-bold w-3 text-center shrink-0 ${statusColors[diff.status]}`}
							>
								{statusLabels[diff.status]}
							</span>
							<span className="truncate font-mono text-[11.5px]">
								{fileName}
							</span>
							{dir && (
								<span className="text-fg-dim truncate font-mono text-[10.5px] ml-auto">
									{dir}
								</span>
							)}
						</div>
					);
				})}
			</div>
		</div>
	);
}
