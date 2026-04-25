import { useCallback, useEffect, useState } from "react";
import type { DiffEntry, ServerMessage } from "../types/protocol";

interface Props {
	visible: boolean;
	onOpenDiff?: (path: string) => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function DiffsPanel({ visible, onOpenDiff, subscribe }: Props) {
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

	if (!visible) return null;

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
			{/* Header */}
			<div className="h-8 px-3 flex items-center shrink-0">
				<span className="text-[11px] text-fg-muted">
					{diffs.length === 0
						? "No changes"
						: `${diffs.length} ${diffs.length === 1 ? "file" : "files"} changed`}
				</span>
			</div>
			<div className="overflow-y-auto flex-1 px-1 pb-2">
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
							className="group flex items-center gap-2 mx-1 px-2 py-1.5 rounded cursor-pointer text-[12px] text-fg-muted hover:bg-bg-hover hover:text-fg transition-colors"
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
