import {
	ChevronDown,
	PanelBottomClose,
	Plus,
	SquareTerminal,
	X,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type {
	ServerMessage,
	ShellEntry,
	TerminalEntry,
} from "../types/protocol";
import { TerminalView } from "./TerminalView";

interface Props {
	onClose: () => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function TerminalPanel({ onClose, subscribe }: Props) {
	const [entries, setEntries] = useState<TerminalEntry[]>([]);
	const [shells, setShells] = useState<ShellEntry[]>([]);
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

	const create = useCallback(async (shell?: string) => {
		if (creatingRef.current) return;
		creatingRef.current = true;
		try {
			const res = await fetch("/api/terminals", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ shell, cols: 80, rows: 24 }),
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
			try {
				const res = await fetch("/api/terminals/shells");
				if (res.ok) setShells((await res.json()) as ShellEntry[]);
			} catch {}
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
			<div className="h-8 flex items-stretch shrink-0">
				<div className="flex-1 min-w-0 flex items-stretch overflow-x-auto">
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
				</div>
				<div className="flex items-stretch shrink-0">
					<NewTerminalButton shells={shells} onCreate={create} />
					<IconButton
						icon={<PanelBottomClose size={13} />}
						title="Hide terminal"
						onClick={onClose}
						className="mr-1"
					/>
				</div>
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

function NewTerminalButton({
	shells,
	onCreate,
}: {
	shells: ShellEntry[];
	onCreate: (shell?: string) => void;
}) {
	const [open, setOpen] = useState(false);
	const ref = useRef<HTMLDivElement>(null);

	useEffect(() => {
		if (!open) return;
		const handler = (e: MouseEvent) => {
			if (!ref.current?.contains(e.target as Node)) setOpen(false);
		};
		document.addEventListener("mousedown", handler);
		return () => document.removeEventListener("mousedown", handler);
	}, [open]);

	const label = shells[0]?.name ?? "shell";

	return (
		<div ref={ref} className="relative flex items-stretch shrink-0">
			<IconButton
				icon={<Plus size={13} />}
				title={`New ${label} terminal`}
				onClick={() => onCreate()}
			/>
			{shells.length > 1 && (
				<button
					type="button"
					className="self-center flex items-center justify-center w-4 h-7 -ml-1.5 rounded-md text-fg-dim hover:text-fg-muted hover:bg-bg-hover cursor-pointer transition-colors shrink-0"
					onClick={() => setOpen((v) => !v)}
					title="New terminal with another shell"
					aria-label="Select shell"
				>
					<ChevronDown size={11} />
				</button>
			)}
			{open && (
				<div className="absolute top-full right-0 mt-1 min-w-[160px] max-h-[220px] overflow-y-auto bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl py-1 z-50">
					{shells.map((shell, i) => (
						<button
							type="button"
							key={shell.id}
							onClick={() => {
								setOpen(false);
								onCreate(shell.id);
							}}
							className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-[11.5px] text-fg-muted hover:bg-bg-hover hover:text-fg cursor-pointer transition-colors"
						>
							<SquareTerminal size={12} className="text-fg-dim shrink-0" />
							<span className="flex-1 truncate">{shell.name}</span>
							{i === 0 && (
								<span className="text-[10px] text-fg-dim">default</span>
							)}
						</button>
					))}
				</div>
			)}
		</div>
	);
}

function IconButton({
	icon,
	title,
	onClick,
	className = "",
}: {
	icon: React.ReactNode;
	title: string;
	onClick: () => void;
	className?: string;
}) {
	return (
		<button
			type="button"
			className={`self-center flex items-center justify-center w-7 h-7 rounded-md text-fg-dim hover:text-fg-muted hover:bg-bg-hover cursor-pointer transition-colors shrink-0 ${className}`}
			onClick={onClick}
			title={title}
		>
			{icon}
		</button>
	);
}
