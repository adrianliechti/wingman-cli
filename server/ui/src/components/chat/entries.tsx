import { ChevronDown, ChevronRight, LoaderCircle } from "lucide-react";
import {
	memo,
	useCallback,
	useContext,
	useEffect,
	useRef,
	useState,
} from "react";
import type { ChatEntry } from "../../hooks/useWebSocket";
import type { Phase } from "../../types/protocol";
import { MarkdownContent } from "../MarkdownContent";
import { ToolProgressContext } from "./progress";

export const EntryView = memo(function EntryView({
	entry,
	isStreaming,
}: {
	entry: ChatEntry;
	isStreaming: boolean;
}) {
	if (entry.type === "reasoning") {
		return <ReasoningView entry={entry} isStreaming={isStreaming} />;
	}

	const isUser = entry.type === "user";

	return (
		<div data-entry-id={entry.id} className="mb-4">
			<div
				className={`border-l-2 ${isUser ? "border-success" : "border-purple"} pl-3 text-[12px] leading-[1.7] break-words min-w-0 font-mono`}
			>
				{isUser ? (
					<>
						{entry.content && (
							<span className="whitespace-pre-wrap text-fg">
								{entry.content}
							</span>
						)}
						{entry.files && entry.files.length > 0 && (
							<div
								className={`flex flex-wrap gap-1 ${entry.content ? "mt-2" : ""}`}
							>
								{entry.files.map((path) => (
									<span
										key={path}
										className="max-w-[240px] truncate rounded bg-bg-active px-1.5 py-0.5 text-[10px] text-fg-muted"
										title={path}
									>
										{path.split("/").pop() || path}
									</span>
								))}
							</div>
						)}
						{entry.images && entry.images.length > 0 && (
							<div
								className={`flex flex-wrap gap-1.5 ${entry.content || (entry.files?.length ?? 0) > 0 ? "mt-2" : ""}`}
							>
								{entry.images.map((src, i) => (
									<a
										key={`${entry.id}-img-${i}`}
										href={src}
										target="_blank"
										rel="noreferrer"
										className="block rounded overflow-hidden bg-bg-active"
									>
										<img
											src={src}
											alt="attachment"
											className="block max-h-40 max-w-[240px] object-contain"
										/>
									</a>
								))}
							</div>
						)}
					</>
				) : (
					<MarkdownContent text={entry.content} streaming={isStreaming} />
				)}
			</div>
		</div>
	);
});

const ReasoningView = memo(function ReasoningView({
	entry,
	isStreaming,
}: {
	entry: ChatEntry;
	isStreaming: boolean;
}) {
	const summary = entry.content || "";
	if (!summary) return null;

	return (
		<div
			data-entry-id={entry.id}
			className="mb-4 border-l-2 border-purple pl-3"
		>
			<div className="text-[11px] whitespace-pre-wrap break-words text-fg-dim font-mono leading-relaxed italic">
				{summary}
				{isStreaming && (
					<span className="inline-block w-[3px] h-[10px] bg-fg-dim/40 align-text-bottom ml-0.5 animate-[pulse_1.4s_ease-in-out_infinite]" />
				)}
			</div>
		</div>
	);
});

export const ToolGroupView = memo(function ToolGroupView({
	entries,
	isTrailing,
	phase,
}: {
	entries: ChatEntry[];
	isTrailing: boolean;
	phase: Phase;
}) {
	return (
		<div className="mb-4">
			{entries.map((entry) => {
				const running =
					isTrailing && phase !== "idle" && entry.toolResult === undefined;
				if (entry.toolName === "todo") {
					return <TodoRow key={entry.id} entry={entry} />;
				}
				return <ToolRow key={entry.id} entry={entry} running={running} />;
			})}
		</div>
	);
});

function TodoRow({ entry }: { entry: ChatEntry }) {
	let items: { content: string; status: string }[] = [];
	try {
		items = JSON.parse(entry.toolArgs || "{}").items ?? [];
	} catch {}

	if (!items.length) {
		return <ToolRow entry={entry} running={false} />;
	}

	const completed = items.filter((it) => it.status === "completed").length;

	return (
		<div data-entry-id={entry.id} className="border-l-2 border-purple pl-3">
			<div className="flex items-center gap-2 py-0.5 text-[12px]">
				<span className="text-purple font-mono text-[11px]">plan</span>
				<span className="text-fg-dim font-mono text-[11px]">
					{completed}/{items.length}
				</span>
			</div>
			<div className="flex flex-col">
				{items.map((it, i) => (
					<div
						key={`${i}-${it.content}`}
						className="flex items-start gap-1.5 text-[12px] font-mono leading-relaxed"
					>
						{it.status === "completed" ? (
							<>
								<span className="text-success">✔</span>
								<span className="text-fg-dim line-through">{it.content}</span>
							</>
						) : it.status === "in_progress" ? (
							<>
								<span className="text-accent">□</span>
								<span className="text-fg font-medium">{it.content}</span>
							</>
						) : (
							<>
								<span className="text-fg-dim">□</span>
								<span className="text-fg-dim">{it.content}</span>
							</>
						)}
					</div>
				))}
			</div>
		</div>
	);
}

function parseArgs(json?: string): Record<string, unknown> | null {
	if (!json) return null;
	try {
		const v: unknown = JSON.parse(json);
		if (v && typeof v === "object" && !Array.isArray(v)) {
			return v as Record<string, unknown>;
		}
	} catch {}
	return null;
}

const ToolRow = memo(function ToolRow({
	entry,
	running,
}: {
	entry: ChatEntry;
	running: boolean;
}) {
	const [expanded, setExpanded] = useState(false);
	const [showAll, setShowAll] = useState(false);
	const [hovered, setHovered] = useState(false);
	const displayHint = entry.toolHint ? truncate(entry.toolHint, 80) : "";
	const isDiff = entry.toolName === "edit" || entry.toolName === "write";
	const isShell =
		entry.toolName === "shell" || entry.toolName === "exec_command";
	const result = (entry.toolResult || "").replace(/\n+$/, "");
	const limit = isDiff ? 4000 : 2000;
	const overflows = result.length > limit;
	const shown = showAll ? result : truncate(result, limit);

	const progressText =
		useContext(ToolProgressContext)[entry.toolId ?? ""] ?? "";
	const args = isDiff ? null : parseArgs(entry.toolArgs);
	const command =
		isShell && typeof args?.command === "string" ? args.command : "";
	const workdir =
		isShell && typeof args?.workdir === "string" ? args.workdir : "";
	const argEntries =
		!command && args
			? Object.entries(args).map(([key, value]) => ({
					key,
					value: truncate(
						typeof value === "string" ? value : JSON.stringify(value),
						600,
					),
				}))
			: [];
	const hasCall = !!command || argEntries.length > 0;

	return (
		<div
			data-entry-id={entry.id}
			onMouseEnter={() => setHovered(true)}
			onMouseLeave={() => setHovered(false)}
		>
			<div className="border-l-2 border-purple pl-3">
				<div
					className="flex items-center gap-2 py-0.5 cursor-pointer text-[12px] transition-colors"
					onClick={() => setExpanded(!expanded)}
				>
					<span className="text-fg-dim shrink-0 flex items-center">
						{running ? (
							<LoaderCircle size={11} className="animate-spin" />
						) : expanded ? (
							<ChevronDown size={12} />
						) : (
							<ChevronRight size={12} />
						)}
					</span>
					<span className="text-purple font-mono text-[11px] shrink-0">
						{isShell ? "$" : entry.toolName}
					</span>
					{displayHint && (
						<span className="text-fg-dim font-mono text-[11px] overflow-hidden text-ellipsis whitespace-nowrap flex-1">
							{displayHint}
						</span>
					)}
				</div>
				{running && progressText && (
					<div className="pl-5 text-[11px] text-fg-dim font-mono italic overflow-hidden text-ellipsis whitespace-nowrap">
						{progressText}
					</div>
				)}
				{expanded && (
					<div
						className={`mt-1 px-3 py-2 text-[11px] whitespace-pre-wrap break-all text-fg-dim bg-bg-surface rounded-md font-mono leading-relaxed ${
							showAll ? "max-h-[50vh] overflow-y-auto overscroll-contain" : ""
						}`}
					>
						{command ? (
							<div className="text-fg">
								<span className="text-purple">$ </span>
								{command}
								{workdir && (
									<span className="text-fg-dim"> [in {workdir}]</span>
								)}
							</div>
						) : (
							argEntries.map(({ key, value }) => (
								<div key={key}>
									<span className="text-fg-muted">{key}:</span> {value}
								</div>
							))
						)}
						{hasCall && <div className="my-1.5 h-px bg-border-subtle" />}
						{result ? (
							isDiff ? (
								<DiffText text={shown} />
							) : (
								shown
							)
						) : (
							<span className="italic opacity-70">(no output)</span>
						)}
					</div>
				)}
			</div>
			{expanded && entry.toolResult && (
				<div className="pl-3 h-4 mt-0.5 flex items-center gap-3">
					{overflows && (
						<button
							type="button"
							className="text-[10px] text-fg-dim hover:text-fg transition-colors cursor-pointer"
							onClick={() => setShowAll((s) => !s)}
						>
							{showAll
								? "Show less"
								: `Show all (${formatChars(result.length)})`}
						</button>
					)}
					{hovered && <CopyTextButton text={entry.toolResult} label="Copy" />}
				</div>
			)}
		</div>
	);
});

function DiffText({ text }: { text: string }) {
	return (
		<>
			{text.split("\n").map((line, i) => (
				<div
					key={`${i}-${line.slice(0, 24)}`}
					className={
						line.startsWith("+")
							? "text-success"
							: line.startsWith("-")
								? "text-danger"
								: undefined
					}
				>
					{line || " "}
				</div>
			))}
		</>
	);
}

function truncate(text: string, max: number): string {
	return text.length <= max ? text : `${text.substring(0, max)}...`;
}

function formatChars(n: number): string {
	if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M chars`;
	if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K chars`;
	return `${n} chars`;
}

function CopyTextButton({
	text,
	label = "Copy",
	className = "",
}: {
	text: string;
	label?: string;
	className?: string;
}) {
	const [copied, setCopied] = useState(false);
	const timer = useRef<number | null>(null);

	useEffect(
		() => () => {
			if (timer.current) window.clearTimeout(timer.current);
		},
		[],
	);

	const handleClick = useCallback(
		(e: React.MouseEvent<HTMLButtonElement>) => {
			e.stopPropagation();
			navigator.clipboard
				.writeText(text)
				.then(() => {
					setCopied(true);
					if (timer.current) window.clearTimeout(timer.current);
					timer.current = window.setTimeout(() => setCopied(false), 1200);
				})
				.catch(() => {});
		},
		[text],
	);

	return (
		<button
			type="button"
			onClick={handleClick}
			className={`text-[10px] text-fg-dim hover:text-fg transition-colors cursor-pointer ${className}`}
		>
			{copied ? "Copied" : label}
		</button>
	);
}
