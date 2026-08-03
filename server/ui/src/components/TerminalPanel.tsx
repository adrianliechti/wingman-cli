import { ChevronDown, Plus, SquareTerminal, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ServerMessage, TerminalEntry } from "../types/protocol";
import { TerminalView } from "./TerminalView";

interface Props {
	onClose: () => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function TerminalPanel({ onClose, subscribe }: Props) {
	const [entries, setEntries] = useState<TerminalEntry[]>([]);
	const [activeId, setActiveId] = useState("");
	const [error, setError] = useState<string | null>(null);

	const onCloseRef = useRef(onClose);
	onCloseRef.current = onClose;
	const hadEntriesRef = useRef(false);
	const creatingRef = useRef(false);
	const bootstrappedRef = useRef(false);

	const reload = useCallback(async (): Promise<TerminalEntry[]> => {
		let list: TerminalEntry[] = [];
		try {
			const res = await fetch("/api/terminals");
			if (res.ok) list = (await res.json()) as TerminalEntry[];
		} catch {}
		setEntries(list);
		setActiveId((prev) =>
			list.some((t) => t.id === prev) ? prev : (list.at(-1)?.id ?? ""),
		);
		if (list.length === 0 && hadEntriesRef.current) onCloseRef.current();
		hadEntriesRef.current = list.length > 0;
		return list;
	}, []);

	const create = useCallback(async () => {
		if (creatingRef.current) return;
		creatingRef.current = true;
		try {
			const res = await fetch("/api/terminals", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ cols: 80, rows: 24 }),
			});
			if (!res.ok) {
				setError((await res.text()).trim() || "Failed to start terminal.");
				return;
			}
			const entry = (await res.json()) as TerminalEntry;
			setError(null);
			hadEntriesRef.current = true;
			setEntries((prev) =>
				prev.some((t) => t.id === entry.id) ? prev : [...prev, entry],
			);
			setActiveId(entry.id);
		} catch {
			setError("Failed to start terminal.");
		} finally {
			creatingRef.current = false;
		}
	}, []);

	const close = useCallback(
		async (id: string) => {
			try {
				await fetch(`/api/terminals/${encodeURIComponent(id)}`, {
					method: "DELETE",
				});
			} catch {}
			await reload();
		},
		[reload],
	);

	useEffect(() => {
		if (bootstrappedRef.current) return;
		bootstrappedRef.current = true;
		void (async () => {
			const list = await reload();
			if (list.length === 0) await create();
		})();
	}, [reload, create]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "terminals_changed") void reload();
		});
	}, [subscribe, reload]);

	return (
		<div className="h-full flex flex-col bg-bg overflow-hidden">
			<div className="h-8 flex items-stretch shrink-0 overflow-x-auto">
				{entries.map((entry) => {
					const active = entry.id === activeId;
					return (
						<div
							key={entry.id}
							className={`group relative flex items-center gap-1.5 px-3 cursor-pointer text-[11px] shrink-0 select-none transition-colors ${
								active ? "text-fg" : "text-fg-dim hover:text-fg-muted"
							}`}
							onClick={() => setActiveId(entry.id)}
						>
							{active && (
								<span className="absolute bottom-0 left-2 right-2 h-[2px] bg-accent rounded-full" />
							)}
							<span className="w-3.5 h-3.5 flex items-center justify-center shrink-0">
								<SquareTerminal
									size={12}
									className={`group-hover:hidden ${active ? "text-fg-muted" : "text-fg-dim"}`}
								/>
								<button
									type="button"
									className="hidden group-hover:flex w-3.5 h-3.5 items-center justify-center text-fg-dim hover:text-fg rounded transition-colors"
									onClick={(e) => {
										e.stopPropagation();
										void close(entry.id);
									}}
									aria-label="Close terminal"
								>
									<X size={11} />
								</button>
							</span>
							<span className="truncate max-w-[160px]">{entry.title}</span>
						</div>
					);
				})}
				<div className="flex-1" />
				<button
					type="button"
					className="self-center flex items-center justify-center w-7 h-7 rounded-md text-fg-dim hover:text-fg-muted hover:bg-bg-hover cursor-pointer transition-colors shrink-0"
					onClick={() => void create()}
					title="New terminal"
				>
					<Plus size={13} />
				</button>
				<button
					type="button"
					className="self-center flex items-center justify-center w-7 h-7 mr-1 rounded-md text-fg-dim hover:text-fg-muted hover:bg-bg-hover cursor-pointer transition-colors shrink-0"
					onClick={onClose}
					title="Hide terminal"
				>
					<ChevronDown size={13} />
				</button>
			</div>
			<div className="h-px bg-border-subtle shrink-0" />
			<div className="relative flex-1 min-h-0">
				{error && (
					<div className="absolute inset-0 flex items-center justify-center px-4 text-[11px] text-fg-dim text-center">
						{error}
					</div>
				)}
				{entries.map((entry) => (
					<div
						key={entry.id}
						className={`absolute inset-0 ${
							entry.id === activeId ? "" : "invisible pointer-events-none"
						}`}
					>
						<TerminalView
							id={entry.id}
							active={entry.id === activeId}
							onExit={() => void reload()}
						/>
					</div>
				))}
			</div>
		</div>
	);
}
