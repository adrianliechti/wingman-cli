import { Loader2, Search, Sparkles } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
	type GraphModules,
	type GraphModuleStat,
	fetchGraphModules,
	fetchGraphSummaries,
} from "../../api/insights";
import { PanZoomCanvas } from "../PanZoomCanvas";

const MAX_MODULES = 300;
const MAX_ROW_WIDTH = 2200;
const CARD_HEIGHT = 40;
const ROW_GAP = 72;
const COL_GAP = 26;
const CHAR_WIDTH = 6.4;
const SECTION_GAP = 56;
const MAX_LEGEND = 8;

const GROUP_PALETTE = [
	"var(--color-info)",
	"var(--color-purple)",
	"var(--color-orange)",
	"var(--color-success)",
	"var(--color-warning)",
	"var(--color-danger)",
];

interface PlacedModule {
	module: GraphModuleStat;
	x: number;
	y: number;
	width: number;
	isolated: boolean;
}

interface PlacedEdge {
	from: string;
	to: string;
	color: string;
	x0: number;
	y0: number;
	x1: number;
	y1: number;
}

interface LegendGroup {
	name: string;
	color: string;
}

function cardWidth(path: string) {
	return Math.round(
		Math.min(280, Math.max(100, path.length * CHAR_WIDTH + 26)),
	);
}

function topSegment(path: string) {
	if (path === "." || path === "") return "./";
	const [first] = path.split("/");
	return first;
}

function wrapRows(modules: GraphModuleStat[]): GraphModuleStat[][] {
	const rows: GraphModuleStat[][] = [];
	let current: GraphModuleStat[] = [];
	let width = 0;
	for (const module of modules) {
		const w = cardWidth(module.path);
		if (current.length > 0 && width + COL_GAP + w > MAX_ROW_WIDTH) {
			rows.push(current);
			current = [];
			width = 0;
		}
		current.push(module);
		width += (current.length > 1 ? COL_GAP : 0) + w;
	}
	if (current.length > 0) rows.push(current);
	return rows;
}

function layoutModules(data: GraphModules) {
	let modules = data.modules;
	let truncated = false;
	if (modules.length > MAX_MODULES) {
		modules = [...modules]
			.sort((a, b) => b.nodes - a.nodes)
			.slice(0, MAX_MODULES);
		truncated = true;
	}
	const present = new Set(modules.map((module) => module.path));
	const edges = data.edges.filter(
		(edge) => present.has(edge.from) && present.has(edge.to),
	);

	const groupNodes = new Map<string, number>();
	for (const module of modules) {
		const group = topSegment(module.path);
		groupNodes.set(group, (groupNodes.get(group) ?? 0) + module.nodes);
	}
	const groupColor = new Map<string, string>();
	const legend: LegendGroup[] = [];
	const sortedGroups = [...groupNodes.entries()].sort((a, b) => b[1] - a[1]);
	sortedGroups.forEach(([name], index) => {
		const color = GROUP_PALETTE[index % GROUP_PALETTE.length];
		groupColor.set(name, color);
		legend.push({ name, color });
	});

	const degree = new Map<string, number>();
	for (const edge of edges) {
		degree.set(edge.from, (degree.get(edge.from) ?? 0) + 1);
		degree.set(edge.to, (degree.get(edge.to) ?? 0) + 1);
	}
	const connected = modules.filter((module) => degree.has(module.path));
	const isolated = modules.filter((module) => !degree.has(module.path));

	const depsOf = new Map<string, string[]>();
	for (const edge of edges) {
		const deps = depsOf.get(edge.from);
		if (deps) deps.push(edge.to);
		else depsOf.set(edge.from, [edge.to]);
	}

	const layerOf = new Map<string, number>();
	const visiting = new Set<string>();
	const layer = (path: string): number => {
		const known = layerOf.get(path);
		if (known !== undefined) return known;
		if (visiting.has(path)) return 0;
		visiting.add(path);
		let value = 0;
		for (const dep of depsOf.get(path) ?? []) {
			value = Math.max(value, layer(dep) + 1);
		}
		visiting.delete(path);
		layerOf.set(path, value);
		return value;
	};
	for (const module of connected) layer(module.path);

	const layers = new Map<number, GraphModuleStat[]>();
	let maxLayer = 0;
	for (const module of connected) {
		const l = layerOf.get(module.path) ?? 0;
		maxLayer = Math.max(maxLayer, l);
		const row = layers.get(l);
		if (row) row.push(module);
		else layers.set(l, [module]);
	}

	const byGroupThenPath = (a: GraphModuleStat, b: GraphModuleStat) => {
		const ga = topSegment(a.path);
		const gb = topSegment(b.path);
		if (ga !== gb) return ga.localeCompare(gb);
		return a.path.localeCompare(b.path);
	};

	const connectedRows: GraphModuleStat[][] = [];
	for (let l = maxLayer; l >= 0; l--) {
		const row = layers.get(l);
		if (!row) continue;
		row.sort(byGroupThenPath);
		connectedRows.push(...wrapRows(row));
	}
	const isolatedRows = wrapRows([...isolated].sort(byGroupThenPath));

	const widthOf = (row: GraphModuleStat[]) =>
		row.reduce((sum, module) => sum + cardWidth(module.path), 0) +
		COL_GAP * (row.length - 1);
	let canvasWidth = 0;
	for (const row of [...connectedRows, ...isolatedRows]) {
		canvasWidth = Math.max(canvasWidth, widthOf(row));
	}

	const placed = new Map<string, PlacedModule>();
	let y = 0;
	for (const row of connectedRows) {
		let x = (canvasWidth - widthOf(row)) / 2;
		for (const module of row) {
			const width = cardWidth(module.path);
			placed.set(module.path, { module, x, y, width, isolated: false });
			x += width + COL_GAP;
		}
		y += CARD_HEIGHT + ROW_GAP;
	}
	let isolatedLabelY: number | null = null;
	if (isolatedRows.length > 0) {
		y += connectedRows.length > 0 ? SECTION_GAP - ROW_GAP : 0;
		isolatedLabelY = y - 24;
		for (const row of isolatedRows) {
			let x = (canvasWidth - widthOf(row)) / 2;
			for (const module of row) {
				const width = cardWidth(module.path);
				placed.set(module.path, { module, x, y, width, isolated: true });
				x += width + COL_GAP;
			}
			y += CARD_HEIGHT + ROW_GAP / 2;
		}
	}

	const placedEdges: PlacedEdge[] = edges.flatMap((edge) => {
		const from = placed.get(edge.from);
		const to = placed.get(edge.to);
		if (!from || !to) return [];
		return [
			{
				from: edge.from,
				to: edge.to,
				color:
					groupColor.get(topSegment(edge.from)) ?? "var(--color-border-strong)",
				x0: from.x + from.width / 2,
				y0: from.y + CARD_HEIGHT,
				x1: to.x + to.width / 2,
				y1: to.y,
			},
		];
	});

	return {
		placed: [...placed.values()],
		edges: placedEdges,
		groupColor,
		legend,
		isolatedLabelY,
		width: canvasWidth,
		height: Math.max(CARD_HEIGHT, y - ROW_GAP / 2),
		truncated,
	};
}

export function ModuleMap({
	initialSelection,
	refreshKey,
	onSearchModule,
}: {
	initialSelection?: string;
	refreshKey: number;
	onSearchModule: (path: string) => void;
}) {
	const [data, setData] = useState<GraphModules | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [selected, setSelected] = useState<string | null>(
		initialSelection ?? null,
	);
	const [hovered, setHovered] = useState<string | null>(null);
	const [summaries, setSummaries] = useState<Record<string, string>>({});
	const [summaryLoading, setSummaryLoading] = useState(false);
	const [summaryError, setSummaryError] = useState<string | null>(null);
	const summaryRequestRef = useRef<AbortController | null>(null);

	useEffect(() => {
		const controller = new AbortController();
		setData(null);
		setError(null);
		fetchGraphModules(controller.signal)
			.then(setData)
			.catch((loadError: unknown) => {
				if (controller.signal.aborted) return;
				setError(
					loadError instanceof Error ? loadError.message : "Load failed",
				);
			});
		return () => controller.abort();
	}, [refreshKey]);

	useEffect(() => {
		if (initialSelection) setSelected(initialSelection);
	}, [initialSelection]);

	useEffect(() => {
		summaryRequestRef.current?.abort();
		summaryRequestRef.current = null;
		setSummaryLoading(false);
		setSummaryError(null);
	}, [selected]);

	useEffect(() => {
		if (!data) return;
		const controller = new AbortController();
		setSummaries({});
		const ordered = [...data.modules]
			.sort((a, b) => b.nodes - a.nodes)
			.slice(0, MAX_MODULES)
			.map((module) => module.path);
		fetchGraphSummaries(ordered, true, controller.signal)
			.then(({ summaries: cached }) =>
				setSummaries((previous) => ({ ...previous, ...cached })),
			)
			.catch(() => {});
		return () => controller.abort();
	}, [data]);

	useEffect(() => () => summaryRequestRef.current?.abort(), []);

	const summarizeSelected = async () => {
		if (!selected) return;
		summaryRequestRef.current?.abort();
		const controller = new AbortController();
		summaryRequestRef.current = controller;
		setSummaryLoading(true);
		setSummaryError(null);
		try {
			const result = await fetchGraphSummaries([selected], false, controller.signal);
			if (controller.signal.aborted) return;
			const summary = result.summaries[selected];
			if (!summary) throw new Error("No description was generated.");
			setSummaries((previous) => ({ ...previous, [selected]: summary }));
		} catch (loadError) {
			if (controller.signal.aborted) return;
			setSummaryError(
				loadError instanceof Error ? loadError.message : "Description failed",
			);
		} finally {
			if (summaryRequestRef.current === controller) {
				summaryRequestRef.current = null;
				setSummaryLoading(false);
			}
		}
	};

	const layout = useMemo(() => (data ? layoutModules(data) : null), [data]);
	useEffect(() => {
		if (
			selected &&
			layout &&
			!layout.placed.some((entry) => entry.module.path === selected)
		) {
			setSelected(null);
		}
	}, [layout, selected]);

	const focus = hovered ?? selected;

	const neighbors = useMemo(() => {
		const set = new Set<string>();
		if (!focus || !layout) return set;
		for (const edge of layout.edges) {
			if (edge.from === focus) set.add(edge.to);
			if (edge.to === focus) set.add(edge.from);
		}
		return set;
	}, [focus, layout]);

	if (error) {
		return (
			<div className="grid h-full place-items-center px-6 text-center text-[11px] text-danger/80">
				{error}
			</div>
		);
	}
	if (!layout) {
		return (
			<div className="flex h-full items-center justify-center gap-1.5 text-[11px] text-fg-dim">
				<Loader2 size={11} className="animate-spin" />
				<span>Loading module map…</span>
			</div>
		);
	}
	if (layout.placed.length === 0) {
		return (
			<div className="grid h-full place-items-center px-6 text-center text-[11px] text-fg-dim">
				<div>
					<div className="mb-1 text-fg-muted">No architecture to map yet</div>
					No indexed definitions were found in this workspace.
				</div>
			</div>
		);
	}

	const selectedModule = selected
		? layout.placed.find((entry) => entry.module.path === selected)?.module
		: undefined;
	const dependsOn = selected
		? layout.edges.filter((edge) => edge.from === selected).length
		: 0;
	const dependedBy = selected
		? layout.edges.filter((edge) => edge.to === selected).length
		: 0;

	return (
		<PanZoomCanvas
			width={layout.width}
			height={layout.height}
			fitKey={data}
			dragExclude="[data-module-card]"
			onBackgroundClick={() => setSelected(null)}
			hud={
				<>
					{selectedModule && (
						<div
							data-canvas-hud
							className="absolute top-3 left-3 z-10 w-80 max-w-[calc(100%-24px)] rounded-lg border border-border bg-bg-elevated/95 p-3 text-[11px] shadow-sm backdrop-blur-sm"
						>
							<div className="truncate font-mono text-[12px] font-medium text-fg">
								{selectedModule.path}
							</div>
							<div className="mt-1 text-[10px] text-fg-dim tabular-nums">
								{selectedModule.files} files · {selectedModule.nodes} symbols ·{" "}
								<span className="text-info">{dependsOn} dependencies</span> ·{" "}
								<span className="text-purple">{dependedBy} dependents</span>
							</div>
							{summaries[selectedModule.path] ? (
								<p className="mt-2 text-[10px] leading-relaxed text-fg-muted">
									{summaries[selectedModule.path]}
								</p>
							) : (
								<button
									type="button"
									disabled={summaryLoading}
									onClick={() => void summarizeSelected()}
									className="mt-2 flex items-center gap-1 text-[10px] text-fg-dim hover:text-fg disabled:opacity-50"
								>
									{summaryLoading ? (
										<Loader2 size={10} className="animate-spin" />
									) : (
										<Sparkles size={10} />
									)}
									Describe this module
								</button>
							)}
							{summaryError && (
								<div className="mt-1 text-[9px] text-danger/80">{summaryError}</div>
							)}
							<div className="mt-2 flex items-center gap-3 border-t border-border-subtle pt-2">
								<button
									type="button"
									onClick={() => onSearchModule(selectedModule.path)}
									className="flex items-center gap-1 text-[10px] text-fg-muted hover:text-fg"
								>
									<Search size={10} /> Find symbols
								</button>
								<span className="ml-auto text-[9px] text-fg-dim">
									<span className="text-info">outgoing</span> ·{" "}
									<span className="text-purple">incoming</span>
								</span>
							</div>
						</div>
					)}
					{layout.legend.length > 1 && (
						<div
							data-canvas-hud
							className="absolute bottom-3 left-3 z-10 flex max-w-[60%] flex-wrap items-center gap-x-2.5 gap-y-1 rounded-md border border-border bg-bg-elevated/95 px-2.5 py-1.5 shadow-sm"
						>
							{layout.legend.slice(0, MAX_LEGEND).map((group) => (
								<span
									key={group.name}
									className="flex items-center gap-1 text-[10px] text-fg-muted"
								>
									<span
										className="h-2 w-2 rounded-full"
										style={{ background: group.color }}
									/>
									{group.name}
								</span>
							))}
							{layout.legend.length > MAX_LEGEND && (
								<span className="text-[10px] text-fg-dim">
									+{layout.legend.length - MAX_LEGEND}
								</span>
							)}
						</div>
					)}
					{layout.truncated && (
						<div
							data-canvas-hud
							className="absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-md border border-warning/30 bg-bg-elevated/95 px-3 py-1.5 text-[11px] text-warning shadow-sm"
						>
							Large workspace — showing the {MAX_MODULES} largest modules.
						</div>
					)}
				</>
			}
		>
			<svg
				aria-hidden="true"
				className="pointer-events-none absolute top-0 left-0 overflow-visible"
				width={Math.max(1, layout.width)}
				height={Math.max(1, layout.height)}
			>
				<defs>
					<marker id="module-arrow-out" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="5" markerHeight="5" orient="auto">
						<path d="M 0 0 L 8 4 L 0 8 z" fill="var(--color-info)" />
					</marker>
					<marker id="module-arrow-in" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="5" markerHeight="5" orient="auto">
						<path d="M 0 0 L 8 4 L 0 8 z" fill="var(--color-purple)" />
					</marker>
				</defs>
				{layout.edges.map((edge) => {
					const outgoing = focus === edge.from;
					const incoming = focus === edge.to;
					const highlighted = outgoing || incoming;
					const color = outgoing
						? "var(--color-info)"
						: incoming
							? "var(--color-purple)"
							: edge.color;
					return (
						<path
							key={`${edge.from}->${edge.to}`}
							d={`M ${edge.x0} ${edge.y0} C ${edge.x0} ${edge.y0 + ROW_GAP / 2}, ${edge.x1} ${edge.y1 - ROW_GAP / 2}, ${edge.x1} ${edge.y1}`}
							fill="none"
							stroke={color}
							strokeWidth={highlighted ? 1.75 : 1}
							markerEnd={
								outgoing
									? "url(#module-arrow-out)"
									: incoming
										? "url(#module-arrow-in)"
										: undefined
							}
							opacity={focus ? (highlighted ? 0.95 : 0.08) : 0.35}
							style={{ transition: "opacity 150ms, stroke 150ms" }}
						/>
					);
				})}
			</svg>
			{layout.isolatedLabelY !== null && (
				<div
					aria-hidden="true"
					className="absolute left-0 text-[10px] uppercase tracking-wider text-fg-dim"
					style={{ top: layout.isolatedLabelY }}
				>
					Standalone modules
				</div>
			)}
			{layout.placed.map(({ module, x, y, width, isolated }) => {
				const active = focus === module.path;
				const dimmed = focus !== null && !active && !neighbors.has(module.path);
				const color = layout.groupColor.get(topSegment(module.path));
				return (
					<div
						key={module.path}
						data-module-card
						role="button"
						tabIndex={0}
						title={[
							`${module.path} · ${module.files} files · ${module.nodes} symbols`,
							summaries[module.path] ?? "",
						]
							.filter(Boolean)
							.join("\n")}
						onClick={(event) => {
							event.stopPropagation();
							setSelected(selected === module.path ? null : module.path);
						}}
						onKeyDown={(event) => {
							if (event.key !== "Enter" && event.key !== " ") return;
							event.preventDefault();
							setSelected(selected === module.path ? null : module.path);
						}}
						onMouseEnter={() => setHovered(module.path)}
						onMouseLeave={() =>
							setHovered((value) => (value === module.path ? null : value))
						}
						className={`absolute cursor-pointer overflow-hidden rounded-lg border shadow-sm transition-[opacity,border-color] duration-150 ${
							selected === module.path
								? "border-accent bg-bg-elevated"
								: "border-border bg-bg-surface hover:border-border-strong"
						} ${dimmed ? "opacity-30" : isolated ? "opacity-60" : ""}`}
						style={{
							left: x,
							top: y,
							width,
							height: CARD_HEIGHT,
							borderLeftWidth: 3,
							borderLeftColor: color,
							paddingInline: 8,
							paddingBlock: 4,
						}}
					>
						<div className="truncate font-mono text-[11px] text-fg">
							{module.path}
						</div>
						<div className="truncate text-[9px] text-fg-dim">
							{summaries[module.path] ?? (
								<span className="tabular-nums">
									{module.files} files · {module.nodes} symbols
								</span>
							)}
						</div>
					</div>
				);
			})}
		</PanZoomCanvas>
	);
}
