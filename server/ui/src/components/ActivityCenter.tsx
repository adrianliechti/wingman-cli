import {
	Activity as ActivityGlyph,
	AlertTriangle,
	Check,
	Code2,
	Download,
	Loader2,
	X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { FloatingSurface } from "./ui/Floating";

export type ActivityState = "running" | "ready" | "error";
export type ActivityIcon = "activity" | "code" | "download";

export interface ActivityOperation {
	label: string;
	detail?: string;
	percentage?: number;
}

// ActivityItem is deliberately independent of any job system. Language
// servers, installers, builds, Git operations, and remote work all normalize
// their own state into this small presentation model.
export interface ActivityItem {
	id: string;
	kind: string;
	state: ActivityState;
	label: string;
	detail?: string;
	scope?: string;
	percentage?: number;
	operations?: ActivityOperation[];
	hint?: string;
	icon?: ActivityIcon;
	dismissible?: boolean;
}

interface Props {
	items: readonly ActivityItem[];
	onDismiss?: (id: string) => void;
	title?: string;
	description?: string;
}

const READY_VISIBILITY_MS = 3500;

export function ActivityCenter({
	items,
	onDismiss,
	title = "Workspace activity",
	description = "Background services and tasks",
}: Props) {
	const [open, setOpen] = useState(false);
	const [recentlyReady, setRecentlyReady] = useState(false);
	const [button, setButton] = useState<HTMLButtonElement | null>(null);
	const wasBusy = useRef(false);
	const readyTimers = useRef<number[]>([]);
	const running = items.filter((item) => item.state === "running");
	const errors = items.filter((item) => item.state === "error");
	const busy = running.length > 0;

	useEffect(() => {
		for (const timer of readyTimers.current) window.clearTimeout(timer);
		readyTimers.current = [];
		if (busy) {
			wasBusy.current = true;
			readyTimers.current.push(
				window.setTimeout(() => setRecentlyReady(false), 0),
			);
			return;
		}
		if (!wasBusy.current) return;
		wasBusy.current = false;
		if (errors.length > 0) {
			readyTimers.current.push(
				window.setTimeout(() => setRecentlyReady(false), 0),
			);
			return;
		}
		readyTimers.current.push(
			window.setTimeout(() => setRecentlyReady(true), 0),
			window.setTimeout(() => setRecentlyReady(false), READY_VISIBILITY_MS),
		);
	}, [busy, errors.length]);

	useEffect(
		() => () => {
			for (const timer of readyTimers.current) window.clearTimeout(timer);
		},
		[],
	);

	if (!busy && errors.length === 0 && !recentlyReady) return null;

	const label = activityLabel(running, errors, recentlyReady);
	const tone =
		errors.length > 0
			? "text-warning hover:text-warning/80"
			: busy
				? "text-fg-dim hover:text-fg-muted"
				: "text-success hover:text-success/80";
	const hints = Array.from(
		new Set(
			items
				.filter((item) => item.state === "running")
				.map((item) => item.hint)
				.filter((hint): hint is string => !!hint),
		),
	);
	const visibleItems =
		busy || errors.length > 0
			? items.filter((item) => item.state !== "ready")
			: items.filter((item) => item.state === "ready");

	return (
		<div
			data-activity-center
			className="flex shrink-0 items-center self-center"
		>
			<button
				ref={setButton}
				type="button"
				data-activity-summary={
					errors.length > 0 ? "error" : busy ? "running" : "ready"
				}
				className={`flex h-7 w-6 items-center justify-center transition-colors ${tone}`}
				title={`${label} · Show ${title.toLowerCase()}`}
				aria-label={`${label}. Show ${title.toLowerCase()}`}
				aria-haspopup="dialog"
				aria-expanded={open}
				onClick={() => setOpen((value) => !value)}
			>
				{errors.length > 0 ? (
					<AlertTriangle size={12} className="shrink-0" />
				) : busy ? (
					<Loader2 size={12} className="shrink-0 animate-spin" />
				) : (
					<Check size={12} className="shrink-0" />
				)}
			</button>
			<FloatingSurface
				open={open}
				onOpenChange={setOpen}
				reference={button}
				placement="bottom-end"
				gap={6}
				role="dialog"
				label={title}
				maxHeight={380}
				className="z-[100] w-80 overflow-hidden rounded-lg border border-border bg-bg-elevated/98 shadow-2xl backdrop-blur-sm"
			>
				<div className="flex items-baseline gap-2 border-b border-border-subtle px-3 py-2">
					<div className="shrink-0 text-[11.5px] font-medium text-fg">
						{title}
					</div>
					<div
						className="min-w-0 flex-1 truncate text-[10px] text-fg-dim"
						title={description}
					>
						{description}
					</div>
				</div>
				<div className="max-h-64 overflow-y-auto p-1.5">
					{visibleItems.map((item) => (
						<ActivityRow
							key={item.id}
							item={item}
							onDismiss={
								item.dismissible && onDismiss
									? () => onDismiss(item.id)
									: undefined
							}
						/>
					))}
					{visibleItems.length === 0 && (
						<div className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-[10.5px] text-fg-muted">
							<Check size={13} className="shrink-0 text-success" />
							Background work is ready.
						</div>
					)}
				</div>
				{hints.map((hint) => (
					<div
						key={hint}
						data-activity-hint
						title={hint}
						className="line-clamp-2 border-t border-border-subtle bg-bg-surface/30 px-3 py-1.5 text-[10px] leading-snug text-fg-dim"
					>
						{hint}
					</div>
				))}
			</FloatingSurface>
		</div>
	);
}

function ActivityRow({
	item,
	onDismiss,
}: {
	item: ActivityItem;
	onDismiss?: () => void;
}) {
	return (
		<div
			data-activity-kind={item.kind}
			data-activity-state={item.state}
			className={`mb-0.5 flex gap-1.5 rounded-md px-2 py-1.5 last:mb-0 ${
				item.state === "error" ? "bg-warning/5" : "hover:bg-bg-surface/40"
			}`}
		>
			<ActivityItemIcon item={item} />
			<div className="min-w-0 flex-1">
				<div className="flex items-baseline gap-1.5">
					<span
						className={`min-w-0 flex-1 truncate text-[11px] ${
							item.state === "error" ? "text-warning" : "text-fg-muted"
						}`}
					>
						{item.label}
					</span>
					{item.scope && (
						<span
							className="max-w-24 shrink-0 truncate font-mono text-[9px] text-fg-dim"
							title={item.scope}
						>
							{item.scope}
						</span>
					)}
				</div>
				{item.detail && (
					<div
						data-activity-detail
						title={item.detail}
						className={`mt-0.5 line-clamp-2 text-[10px] leading-snug ${
							item.state === "ready" ? "text-success/80" : "text-fg-dim"
						}`}
					>
						{item.detail}
					</div>
				)}
				{item.operations?.map((operation, index) => (
					<ActivityOperationRow
						key={`${operation.label}:${index}`}
						operation={operation}
					/>
				))}
				{item.percentage !== undefined && (
					<ProgressBar percentage={item.percentage} />
				)}
			</div>
			{onDismiss && (
				<button
					type="button"
					onClick={onDismiss}
					className="flex h-5 w-5 shrink-0 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
					aria-label={`Dismiss ${item.label}`}
				>
					<X size={11} />
				</button>
			)}
		</div>
	);
}

function ActivityItemIcon({ item }: { item: ActivityItem }) {
	if (item.state === "error") {
		return <AlertTriangle size={13} className="mt-0.5 shrink-0 text-warning" />;
	}
	if (item.state === "ready") {
		return <Check size={13} className="mt-0.5 shrink-0 text-success" />;
	}
	if (item.icon === "download") {
		return <Download size={13} className="mt-0.5 shrink-0 text-accent" />;
	}
	if (item.icon === "code") {
		return <Code2 size={13} className="mt-0.5 shrink-0 text-accent" />;
	}
	return <ActivityGlyph size={13} className="mt-0.5 shrink-0 text-accent" />;
}

function ActivityOperationRow({ operation }: { operation: ActivityOperation }) {
	return (
		<div className="mt-0.5">
			<div className="flex items-center gap-1 text-[10px] text-fg-dim">
				<Loader2 size={9} className="shrink-0 animate-spin" />
				<span className="min-w-0 flex-1 truncate">
					{operation.label}
					{operation.detail ? ` · ${operation.detail}` : ""}
				</span>
				{operation.percentage !== undefined && (
					<span className="shrink-0 tabular-nums">{operation.percentage}%</span>
				)}
			</div>
			{operation.percentage !== undefined && (
				<ProgressBar percentage={operation.percentage} />
			)}
		</div>
	);
}

function ProgressBar({ percentage }: { percentage: number }) {
	return (
		<div className="mt-1 h-0.5 overflow-hidden rounded-full bg-bg-active">
			<div
				className="h-full rounded-full bg-accent transition-[width] duration-300"
				style={{ width: `${Math.max(0, Math.min(100, percentage))}%` }}
			/>
		</div>
	);
}

function activityLabel(
	running: readonly ActivityItem[],
	errors: readonly ActivityItem[],
	recentlyReady: boolean,
) {
	if (errors.length > 0) return errors[0].label;
	if (running.length > 1) return `${running.length} background tasks`;
	if (running.length === 1) return running[0].label;
	if (recentlyReady) return "Background tasks ready";
	return "Workspace activity";
}
