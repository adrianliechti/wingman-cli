import {
	Bot,
	Bug,
	FileText,
	GitCompare,
	Lightbulb,
	Loader2,
	MessageSquare,
	SquareTerminal,
	X,
} from "lucide-react";

export type TabKind =
	| "chat"
	| "file"
	| "diff"
	| "compare"
	| "terminal"
	| "debug"
	| "task"
	| "graph";

export function Tab({
	id,
	kind,
	label,
	active,
	preview,
	closable,
	dirty,
	running,
	position,
	count,
	onActivate,
	onNavigate,
	onClose,
	onKeepOpen,
	onDragStart,
	onDragEnd,
}: {
	id: string;
	kind: TabKind;
	label: string;
	active: boolean;
	preview: boolean;
	closable: boolean;
	dirty: boolean;
	running: boolean;
	position: number;
	count: number;
	onActivate: () => void;
	onNavigate: (position: number) => void;
	onClose: () => void;
	onKeepOpen: () => void;
	onDragStart?: () => void;
	onDragEnd?: () => void;
}) {
	const Icon =
		kind === "chat"
			? MessageSquare
			: kind === "debug"
				? Bug
				: kind === "terminal"
					? SquareTerminal
					: kind === "diff" || kind === "compare"
						? GitCompare
						: kind === "task"
							? Bot
							: kind === "graph"
								? Lightbulb
								: FileText;

	return (
		<button
			type="button"
			role="tab"
			aria-selected={active}
			tabIndex={active ? 0 : -1}
			data-center-tab={id}
			data-tab-preview={preview || undefined}
			draggable={!!onDragStart}
			onDragStart={(event) => {
				event.dataTransfer.setData("text/plain", id);
				event.dataTransfer.effectAllowed = "move";
				onDragStart?.();
			}}
			onDragEnd={onDragEnd}
			className={`group relative flex shrink-0 items-center gap-1.5 px-3 py-0 text-[12px] transition-colors ${
				active ? "text-fg" : "text-fg-dim hover:text-fg-muted"
			} ${active ? "bg-bg-surface/50" : ""} ${preview ? "italic" : ""}`}
			onClick={(event) => {
				if ((event.target as Element).closest("[data-tab-close]")) {
					onClose();
					return;
				}
				onActivate();
			}}
			onKeyDown={(event) => {
				let next = position;
				if (event.key === "ArrowLeft") next = (position - 1 + count) % count;
				else if (event.key === "ArrowRight") next = (position + 1) % count;
				else if (event.key === "Home") next = 0;
				else if (event.key === "End") next = count - 1;
				else if (event.key === "Delete" && closable) {
					event.preventDefault();
					onClose();
					return;
				} else return;
				event.preventDefault();
				onNavigate(next);
			}}
			onDoubleClick={onKeepOpen}
			title={label}
			aria-label={`${label}${preview ? ", preview" : ""}${dirty ? ", unsaved changes" : ""}${closable ? ". Press Delete to close." : ""}`}
		>
			{active && (
				<span className="absolute inset-x-2 bottom-0 h-[2px] rounded-full bg-accent" />
			)}
			<span className="flex h-3.5 w-3.5 shrink-0 items-center justify-center">
				{running ? (
					<Loader2
						size={13}
						className={`${closable ? "group-hover:hidden" : ""} text-accent animate-spin`}
					/>
				) : dirty ? (
					<span
						className={`${closable ? "group-hover:hidden" : ""} h-2 w-2 rounded-full ${active ? "bg-fg-muted" : "bg-fg-dim"}`}
					/>
				) : (
					<Icon
						size={13}
						className={`${closable ? "group-hover:hidden" : ""} ${active ? "text-fg-muted" : "text-fg-dim"}`}
					/>
				)}
				{closable && (
					<span
						data-tab-close
						aria-hidden="true"
						title={`Close ${label}`}
						className="hidden h-3.5 w-3.5 items-center justify-center rounded text-fg-dim transition-colors hover:text-fg group-hover:flex"
					>
						<X size={11} />
					</span>
				)}
			</span>
			<span className="max-w-[200px] truncate">{label}</span>
		</button>
	);
}
