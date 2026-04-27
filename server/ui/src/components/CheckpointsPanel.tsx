import { History, Undo2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { CheckpointEntry, ServerMessage } from "../types/protocol";

interface Props {
	visible: boolean;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function CheckpointsPanel({ visible, subscribe }: Props) {
	const [checkpoints, setCheckpoints] = useState<CheckpointEntry[]>([]);
	const [restoring, setRestoring] = useState<string | null>(null);

	const load = useCallback(async () => {
		try {
			const res = await fetch("/api/checkpoints");
			if (!res.ok) {
				setCheckpoints([]);
				return;
			}
			const data: CheckpointEntry[] = await res.json();
			setCheckpoints(data);
		} catch {
			setCheckpoints([]);
		}
	}, []);

	useEffect(() => {
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "checkpoints_changed") {
				load();
			}
		});
	}, [subscribe, load]);

	const restore = useCallback(
		async (cp: CheckpointEntry) => {
			const ok = window.confirm(
				`Restore working tree to "${cp.message}"?\n\nThis will overwrite uncommitted changes.`,
			);
			if (!ok) return;
			setRestoring(cp.hash);
			try {
				await fetch(
					`/api/checkpoints/${encodeURIComponent(cp.hash)}/restore`,
					{ method: "POST" },
				);
			} finally {
				setRestoring(null);
			}
		},
		[],
	);

	if (!visible) return null;

	return (
		<div className="flex flex-col h-full overflow-hidden bg-bg">
			<div className="h-8 px-3 flex items-center shrink-0">
				<span className="text-[11px] text-fg-muted">Checkpoints</span>
			</div>
			<div className="overflow-y-auto flex-1 px-1 pb-2">
				{checkpoints.length === 0 && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						No checkpoints yet. A checkpoint is created after each agent turn.
					</div>
				)}
				{checkpoints.map((cp, i) => {
					const isLatest = i === 0;
					return (
						<div
							key={cp.hash}
							className="group flex items-center gap-2 mx-1 px-2 py-1.5 rounded text-[12px] text-fg-muted hover:bg-bg-hover hover:text-fg transition-colors"
							title={cp.hash}
						>
							<History
								size={11}
								className={isLatest ? "text-accent" : "text-fg-dim shrink-0"}
							/>
							<div className="flex flex-col min-w-0 flex-1">
								<span className="truncate text-[12px]">
									{cp.message || "(no message)"}
								</span>
								<span className="text-fg-dim text-[10.5px] font-mono truncate">
									{cp.time}
								</span>
							</div>
							<button
								type="button"
								className="w-5 h-5 flex items-center justify-center rounded text-fg-dim hover:text-fg hover:bg-bg cursor-pointer transition-colors opacity-0 group-hover:opacity-100 disabled:opacity-50"
								onClick={() => restore(cp)}
								disabled={restoring !== null}
								title="Restore working tree to this checkpoint"
							>
								<Undo2 size={12} />
							</button>
						</div>
					);
				})}
			</div>
		</div>
	);
}
