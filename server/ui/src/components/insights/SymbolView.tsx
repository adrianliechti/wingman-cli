import { ArrowLeft, ExternalLink, Loader2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
	type GraphNeighborhood,
	type GraphNode,
	fetchGraphSymbol,
} from "../../api/insights";
import { PanZoomCanvas } from "../PanZoomCanvas";
import { nodeLocation, nodeTargetLine } from "./nodes";
import { KindBadge } from "./shared";

const SIDE_WIDTH = 210;
const SIDE_HEIGHT = 40;
const SIDE_GAP = 10;
const CENTER_WIDTH = 260;
const CENTER_HEIGHT = 84;
const COLUMN_GAP = 90;
const MAX_SIDE = 40;
const HEADER_HEIGHT = 30;

interface SideEntry {
	node?: GraphNode;
	more?: number;
	y: number;
}

function sideColumn(nodes: GraphNode[], totalHeight: number): SideEntry[] {
	const shown = nodes.slice(0, MAX_SIDE);
	const rows = shown.length + (nodes.length > MAX_SIDE ? 1 : 0);
	const height = rows * (SIDE_HEIGHT + SIDE_GAP) - SIDE_GAP;
	let y = (totalHeight - height) / 2;
	const entries: SideEntry[] = [];
	for (const node of shown) {
		entries.push({ node, y });
		y += SIDE_HEIGHT + SIDE_GAP;
	}
	if (nodes.length > MAX_SIDE) {
		entries.push({ more: nodes.length - MAX_SIDE, y });
	}
	return entries;
}

function columnHeight(count: number) {
	const rows = Math.min(count, MAX_SIDE) + (count > MAX_SIDE ? 1 : 0);
	return rows > 0 ? rows * (SIDE_HEIGHT + SIDE_GAP) - SIDE_GAP : 0;
}

function RelationChips({
	label,
	nodes,
	onExplore,
}: {
	label: string;
	nodes: GraphNode[];
	onExplore: (node: GraphNode) => void;
}) {
	if (nodes.length === 0) return null;
	return (
		<div className="flex min-w-0 items-center gap-1.5">
			<span className="shrink-0 text-[10px] text-fg-dim">{label}</span>
			<div className="flex min-w-0 flex-wrap items-center gap-1">
				{nodes.map((node) => (
					<button
						key={node.id}
						type="button"
						title={nodeLocation(node)}
						onClick={() => onExplore(node)}
						className="flex max-w-48 items-center gap-1 rounded-full border border-border bg-bg-surface px-1.5 py-0.5 text-[10px] text-fg-muted hover:border-border-strong hover:text-fg"
					>
						<KindBadge kind={node.kind} size={12} />
						<span className="truncate">{node.name}</span>
					</button>
				))}
			</div>
		</div>
	);
}

function SideCard({
	entry,
	x,
	onExplore,
	onOpen,
}: {
	entry: SideEntry;
	x: number;
	onExplore: (node: GraphNode) => void;
	onOpen: (node: GraphNode) => void;
}) {
	if (!entry.node) {
		return (
			<div
				className="absolute grid place-items-center rounded-lg border border-dashed border-border text-[10px] text-fg-dim"
				style={{
					left: x,
					top: entry.y,
					width: SIDE_WIDTH,
					height: SIDE_HEIGHT,
				}}
			>
				+{entry.more} more
			</div>
		);
	}
	const node = entry.node;
	return (
		<div
			data-symbol-card
			role="button"
			tabIndex={0}
			title={`Explore ${node.name} · ${nodeLocation(node)}`}
			onClick={(event) => {
				event.stopPropagation();
				onExplore(node);
			}}
			onKeyDown={(event) => {
				if (event.key !== "Enter" && event.key !== " ") return;
				event.preventDefault();
				onExplore(node);
			}}
			className="group absolute cursor-pointer overflow-hidden rounded-lg border border-border bg-bg-surface px-2 py-1 shadow-sm hover:border-border-strong"
			style={{ left: x, top: entry.y, width: SIDE_WIDTH, height: SIDE_HEIGHT }}
		>
			<div className="flex items-center gap-1.5">
				<KindBadge kind={node.kind} />
				<span className="min-w-0 flex-1 truncate text-[11px] text-fg">
					{node.name}
				</span>
				<button
					type="button"
					title={`Open ${nodeLocation(node)}`}
					aria-label={`Open ${nodeLocation(node)}`}
					onClick={(event) => {
						event.stopPropagation();
						onOpen(node);
					}}
					className="hidden h-4 w-4 shrink-0 place-items-center rounded text-fg-dim group-hover:grid hover:bg-bg-hover hover:text-fg"
				>
					<ExternalLink size={10} />
				</button>
			</div>
			<div className="truncate pl-5 font-mono text-[9px] text-fg-dim">
				{node.file}
			</div>
		</div>
	);
}

export function SymbolView({
	focus,
	refreshKey,
	canGoBack,
	onBack,
	onExplore,
	onOpenFile,
}: {
	focus: { id?: string; name?: string; file?: string } | null;
	refreshKey: number;
	canGoBack: boolean;
	onBack: () => void;
	onExplore: (node: GraphNode) => void;
	onOpenFile: (path: string, line?: number) => void;
}) {
	const [neighborhood, setNeighborhood] = useState<GraphNeighborhood | null>(
		null,
	);
	const [loading, setLoading] = useState(false);
	const [error, setError] = useState<string | null>(null);

	useEffect(() => {
		if (!focus) {
			setNeighborhood(null);
			return;
		}
		const controller = new AbortController();
		setLoading(true);
		setError(null);
		setNeighborhood(null);
		fetchGraphSymbol(focus, controller.signal)
			.then((result) => {
				if (controller.signal.aborted) return;
				setNeighborhood(result);
				setLoading(false);
			})
			.catch((loadError: unknown) => {
				if (controller.signal.aborted) return;
				setError(
					loadError instanceof Error ? loadError.message : "Load failed",
				);
				setLoading(false);
			});
		return () => controller.abort();
	}, [focus, refreshKey]);

	const layout = useMemo(() => {
		if (!neighborhood) return null;
		const callers = neighborhood.callers ?? [];
		const callees = neighborhood.callees ?? [];
		const bodyHeight = Math.max(
			columnHeight(callers.length),
			columnHeight(callees.length),
			CENTER_HEIGHT,
		);
		const centerX = SIDE_WIDTH + COLUMN_GAP;
		const offset = (y: number) => y + HEADER_HEIGHT;
		return {
			height: bodyHeight + HEADER_HEIGHT,
			centerX,
			centerY: offset((bodyHeight - CENTER_HEIGHT) / 2),
			calleeX: centerX + CENTER_WIDTH + COLUMN_GAP,
			width: centerX + CENTER_WIDTH + COLUMN_GAP + SIDE_WIDTH,
			callerEntries: sideColumn(callers, bodyHeight).map((entry) => ({
				...entry,
				y: offset(entry.y),
			})),
			calleeEntries: sideColumn(callees, bodyHeight).map((entry) => ({
				...entry,
				y: offset(entry.y),
			})),
		};
	}, [neighborhood]);

	const openNode = (node: GraphNode) =>
		onOpenFile(node.file, nodeTargetLine(node));

	if (!focus) {
		return (
			<div className="grid h-full place-items-center px-6 text-center text-[11px] text-fg-dim">
				<div>
					<div className="mb-1 text-fg-muted">No symbol selected</div>
					Search for a symbol or pick a hotspot on the overview to explore its
					call graph.
				</div>
			</div>
		);
	}
	if (error) {
		return (
			<div className="relative grid h-full place-items-center px-6 text-center text-[11px] text-danger/80">
				{canGoBack && (
					<button
						type="button"
						onClick={onBack}
						className="absolute top-3 left-3 flex items-center gap-1 rounded-md border border-border bg-bg-elevated px-2 py-1 text-[10px] text-fg-muted hover:bg-bg-hover hover:text-fg"
					>
						<ArrowLeft size={11} /> Back to search
					</button>
				)}
				<div>
					<div className="mb-1 text-fg-muted">Could not load this symbol</div>
					{error}
				</div>
			</div>
		);
	}
	if (!neighborhood || !layout) {
		return (
			<div className="relative flex h-full items-center justify-center gap-1.5 text-[11px] text-fg-dim">
				{canGoBack && (
					<button
						type="button"
						onClick={onBack}
						aria-label="Back to search"
						className="absolute top-3 left-3 grid h-6 w-6 place-items-center rounded-md border border-border bg-bg-elevated text-fg-dim hover:bg-bg-hover hover:text-fg"
					>
						<ArrowLeft size={12} />
					</button>
				)}
				<Loader2 size={11} className="animate-spin" />
				<span>{loading ? "Loading relationships…" : "Loading symbol…"}</span>
			</div>
		);
	}

	const node = neighborhood.node;
	const centerMidY = layout.centerY + CENTER_HEIGHT / 2;

	return (
		<div className="flex h-full min-h-0 flex-col">
			<div className="relative min-h-0 flex-1">
				<PanZoomCanvas
					width={layout.width}
					height={layout.height}
					fitKey={neighborhood}
					dragExclude="[data-symbol-card]"
					hud={
						<div
							data-canvas-hud
							className="absolute top-3 left-3 z-10 flex items-center gap-1.5"
						>
							{canGoBack && (
								<button
									type="button"
									title="Back"
									aria-label="Back"
									onClick={onBack}
									className="grid h-6 w-6 place-items-center rounded-md border border-border bg-bg-elevated/95 text-fg-dim shadow-sm hover:bg-bg-hover hover:text-fg"
								>
									<ArrowLeft size={12} />
								</button>
							)}
							<span className="rounded-md border border-border bg-bg-elevated/95 px-2 py-1 text-[10px] text-fg-dim shadow-sm">
								{(neighborhood.callers ?? []).length} callers ·{" "}
								{(neighborhood.callees ?? []).length} callees
							</span>
						</div>
					}
				>
					<svg
						aria-hidden="true"
						className="pointer-events-none absolute top-0 left-0 overflow-visible"
						width={Math.max(1, layout.width)}
						height={Math.max(1, layout.height)}
					>
						{layout.callerEntries.map((entry, index) => {
							const y0 = entry.y + SIDE_HEIGHT / 2;
							const x0 = SIDE_WIDTH;
							const x1 = layout.centerX;
							const mx = (x0 + x1) / 2;
							return (
								<path
									key={entry.node?.id ?? `more:${index}`}
									d={`M ${x0} ${y0} C ${mx} ${y0}, ${mx} ${centerMidY}, ${x1} ${centerMidY}`}
									fill="none"
									stroke="var(--color-purple)"
									strokeWidth={1.25}
									opacity={0.55}
								/>
							);
						})}
						{layout.calleeEntries.map((entry, index) => {
							const y1 = entry.y + SIDE_HEIGHT / 2;
							const x0 = layout.centerX + CENTER_WIDTH;
							const x1 = layout.calleeX;
							const mx = (x0 + x1) / 2;
							return (
								<path
									key={entry.node?.id ?? `more:${index}`}
									d={`M ${x0} ${centerMidY} C ${mx} ${centerMidY}, ${mx} ${y1}, ${x1} ${y1}`}
									fill="none"
									stroke="var(--color-info)"
									strokeWidth={1.25}
									opacity={0.55}
								/>
							);
						})}
					</svg>
					{layout.callerEntries.map((entry, index) => (
						<SideCard
							key={entry.node?.id ?? `more:${index}`}
							entry={entry}
							x={0}
							onExplore={onExplore}
							onOpen={openNode}
						/>
					))}
					<div
						data-symbol-card
						className="absolute overflow-hidden rounded-lg border border-accent bg-bg-elevated px-3 py-2 shadow-sm"
						style={{
							left: layout.centerX,
							top: layout.centerY,
							width: CENTER_WIDTH,
							height: CENTER_HEIGHT,
						}}
					>
						<div className="flex items-center gap-1.5">
							<KindBadge kind={node.kind} size={16} />
							<span className="min-w-0 flex-1 truncate text-[13px] font-medium text-fg">
								{node.name}
							</span>
						</div>
						<div className="mt-1 truncate font-mono text-[10px] text-fg-dim">
							{nodeLocation(node)}
						</div>
						<button
							type="button"
							onClick={() => openNode(node)}
							className="mt-2 flex items-center gap-1 rounded border border-border px-1.5 py-0.5 text-[10px] text-fg-muted hover:border-border-strong hover:text-fg"
						>
							<ExternalLink size={10} />
							Open source
						</button>
					</div>
					{layout.calleeEntries.map((entry, index) => (
						<SideCard
							key={entry.node?.id ?? `more:${index}`}
							entry={entry}
							x={layout.calleeX}
							onExplore={onExplore}
							onOpen={openNode}
						/>
					))}
					<div
						aria-hidden="true"
						className="absolute text-[10px] uppercase tracking-wider text-purple/80"
						style={{ left: 0, top: 0 }}
					>
						Callers
					</div>
					<div
						aria-hidden="true"
						className="absolute text-[10px] uppercase tracking-wider text-info/80"
						style={{ left: layout.calleeX, top: 0 }}
					>
						Callees
					</div>
				</PanZoomCanvas>
			</div>
			{((neighborhood.extends?.length ?? 0) > 0 ||
				(neighborhood.subtypes?.length ?? 0) > 0 ||
				(neighborhood.implements?.length ?? 0) > 0 ||
				(neighborhood.implementers?.length ?? 0) > 0 ||
				(neighborhood.others?.length ?? 0) > 0) && (
				<div className="flex max-h-32 shrink-0 flex-wrap items-center gap-x-4 gap-y-1.5 overflow-y-auto border-t border-border-subtle bg-bg-surface/20 px-3 py-2">
					<RelationChips
						label="Extends"
						nodes={neighborhood.extends ?? []}
						onExplore={onExplore}
					/>
					<RelationChips
						label="Subtypes"
						nodes={neighborhood.subtypes ?? []}
						onExplore={onExplore}
					/>
					<RelationChips
						label="Implements"
						nodes={neighborhood.implements ?? []}
						onExplore={onExplore}
					/>
					<RelationChips
						label="Implemented by"
						nodes={neighborhood.implementers ?? []}
						onExplore={onExplore}
					/>
					<RelationChips
						label="Same name"
						nodes={neighborhood.others ?? []}
						onExplore={onExplore}
					/>
				</div>
			)}
		</div>
	);
}
