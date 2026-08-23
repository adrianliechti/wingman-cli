import { ChevronDown, ChevronRight } from "lucide-react";
import { memo, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { ChatEntry } from "../../hooks/useWebSocket";
import { sampleSpinnerVerb } from "../../spinnerVerbs";
import type { Phase } from "../../types/protocol";
import { EntryView, ToolGroupView } from "./entries";
import type { Turn } from "./turns";

interface TurnViewProps {
	turn: Turn;
	isActive: boolean;
	phase: Phase;
	applyPendingAnchor: () => void;
	onOpenFile?: (path: string, line?: number) => void;
}

export const TurnView = memo(function TurnView({
	turn,
	isActive,
	phase,
	applyPendingAnchor,
	onOpenFile,
}: TurnViewProps) {
	const canCollapse =
		!isActive && turn.final !== null && turn.working.length > 0;
	const [override, setOverride] = useState<boolean | null>(null);
	const expanded = override ?? !canCollapse;
	// Manual toggle keeps scrollTop unchanged: the disclosure header sits above
	// the content that grows/shrinks, so it stays visually fixed and the steps
	// reveal/hide below it — no scroll jump. (applyPendingAnchor still runs in
	// the effect to preserve position on auto-collapse when a turn finishes.)
	const setExpanded = (value: boolean) => {
		setOverride(value);
	};

	const prevExpandedRef = useRef(expanded);
	useLayoutEffect(() => {
		if (prevExpandedRef.current === expanded) return;
		prevExpandedRef.current = expanded;
		applyPendingAnchor();
	}, [expanded, applyPendingAnchor]);

	return (
		<>
			{turn.user && <EntryView entry={turn.user} isStreaming={false} />}
			{turn.working.length > 0 &&
				(expanded ? (
					<>
						<WorkingExpanded
							entries={turn.working}
							isActive={isActive}
							phase={phase}
							canCollapse={canCollapse}
							onCollapse={() => setExpanded(false)}
							onOpenFile={onOpenFile}
						/>
						{isActive &&
							!(phase === "streaming" && turn.final?.type === "assistant") &&
							turn.working[turn.working.length - 1]?.type !== "reasoning" && (
								<PhaseIndicator />
							)}
					</>
				) : (
					<WorkingHeader
						entries={turn.working}
						expanded={false}
						onToggle={() => setExpanded(true)}
						className="mb-4"
					/>
				))}
			{turn.final && (
				<EntryView
					entry={turn.final}
					isStreaming={
						isActive && phase === "streaming" && turn.final.type === "assistant"
					}
				/>
			)}
			{isActive && turn.working.length === 0 && !turn.final && (
				<PhaseIndicator />
			)}
		</>
	);
}, areTurnPropsEqual);

function areTurnPropsEqual(prev: TurnViewProps, next: TurnViewProps) {
	if (!sameTurnEntries(prev.turn, next.turn)) return false;
	if (prev.isActive !== next.isActive) return false;
	if (prev.onOpenFile !== next.onOpenFile) return false;
	if (!prev.isActive && !next.isActive) return true;
	return (
		prev.phase === next.phase &&
		prev.turn.working.length === next.turn.working.length
	);
}

function sameTurnEntries(a: Turn, b: Turn) {
	if (a.key !== b.key || a.user !== b.user || a.final !== b.final) return false;
	if (a.working.length !== b.working.length) return false;
	for (let i = 0; i < a.working.length; i++) {
		if (a.working[i] !== b.working[i]) return false;
	}
	return true;
}

const WorkingExpanded = memo(function WorkingExpanded({
	entries,
	isActive,
	phase,
	canCollapse,
	onCollapse,
	onOpenFile,
}: {
	entries: ChatEntry[];
	isActive: boolean;
	phase: Phase;
	canCollapse: boolean;
	onCollapse: () => void;
	onOpenFile?: (path: string, line?: number) => void;
}) {
	const nodes: React.ReactNode[] = [];
	let i = 0;
	while (i < entries.length) {
		const entry = entries[i];
		if (entry.type === "tool") {
			const start = i;
			while (i < entries.length && entries[i].type === "tool") i++;
			const slice = entries.slice(start, i);
			const isTrailing = isActive && i === entries.length;
			nodes.push(
				<ToolGroupView
					key={slice[0].id}
					entries={slice}
					isTrailing={isTrailing}
					phase={phase}
					onOpenFile={onOpenFile}
				/>,
			);
			continue;
		}
		const isLastWorking = i === entries.length - 1;
		const isStreaming =
			isActive &&
			isLastWorking &&
			phase === "thinking" &&
			entry.type === "reasoning";
		nodes.push(
			<EntryView key={entry.id} entry={entry} isStreaming={isStreaming} />,
		);
		i++;
	}

	return (
		<>
			{canCollapse && (
				<WorkingHeader
					entries={entries}
					expanded
					onToggle={onCollapse}
					className="mb-1"
				/>
			)}
			{nodes}
		</>
	);
});

function workingSummaryText(entries: ChatEntry[]): string {
	const tools = entries.filter((e) => e.type === "tool").length;
	const thoughts = entries.filter((e) => e.type === "reasoning").length;
	const parts: string[] = [];
	if (thoughts) parts.push(`${thoughts} thought${thoughts === 1 ? "" : "s"}`);
	if (tools) parts.push(`${tools} tool${tools === 1 ? "" : "s"}`);
	return parts.length > 0 ? parts.join(", ") : "Worked";
}

// Disclosure header for the working steps. Same row toggles both ways:
// down-chevron when expanded (steps shown below), right-chevron when collapsed.
const WorkingHeader = memo(function WorkingHeader({
	entries,
	expanded,
	onToggle,
	className = "",
}: {
	entries: ChatEntry[];
	expanded: boolean;
	onToggle: () => void;
	className?: string;
}) {
	return (
		<button
			type="button"
			onClick={onToggle}
			className={`flex items-center gap-2 py-0.5 cursor-pointer text-[11px] text-fg-dim hover:text-fg transition-colors ${className}`}
		>
			{expanded ? (
				<ChevronDown size={12} className="shrink-0" />
			) : (
				<ChevronRight size={12} className="shrink-0" />
			)}
			<span className="font-mono">{workingSummaryText(entries)}</span>
		</button>
	);
});

function PhaseIndicator() {
	const [verb] = useState(sampleSpinnerVerb);

	const [clock, setClock] = useState(() => {
		const startedAt = Date.now();
		return { startedAt, now: startedAt };
	});
	useEffect(() => {
		const id = setInterval(() => {
			setClock((prev) => ({ ...prev, now: Date.now() }));
		}, 1000);
		return () => clearInterval(id);
	}, []);

	const elapsed = Math.max(0, Math.floor((clock.now - clock.startedAt) / 1000));

	return (
		<div className="mb-4 pl-3">
			<div className="flex items-baseline gap-1.5 text-[12px] text-fg-dim font-mono italic">
				<span>{verb}</span>
				<span className="inline-flex gap-[3px]">
					<span
						className="w-[3px] h-[3px] rounded-full bg-fg-dim animate-pulse"
						style={{ animationDelay: "0ms", animationDuration: "1.2s" }}
					/>
					<span
						className="w-[3px] h-[3px] rounded-full bg-fg-dim animate-pulse"
						style={{ animationDelay: "200ms", animationDuration: "1.2s" }}
					/>
					<span
						className="w-[3px] h-[3px] rounded-full bg-fg-dim animate-pulse"
						style={{ animationDelay: "400ms", animationDuration: "1.2s" }}
					/>
				</span>
				<span className="not-italic text-fg-dim/70">
					{elapsed}s · esc to interrupt
				</span>
			</div>
		</div>
	);
}
