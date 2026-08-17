import { Loader2, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
	type GraphModules,
	type GraphModuleStat,
	fetchGraphModules,
} from "../../api/graph";
import { PanZoomCanvas } from "../PanZoomCanvas";

const MAX_MODULES = 300;
const CARD_HEIGHT = 40;
const ROW_GAP = 72;
const COL_GAP = 26;
const CHAR_WIDTH = 6.4;

interface PlacedModule {
	module: GraphModuleStat;
	x: number;
	y: number;
	width: number;
}

interface PlacedEdge {
	from: string;
	to: string;
	x0: number;
	y0: number;
	x1: number;
	y1: number;
}

function cardWidth(path: string) {
	return Math.round(
		Math.min(280, Math.max(100, path.length * CHAR_WIDTH + 26)),
	);
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
	for (const module of modules) layer(module.path);

	const rows = new Map<number, GraphModuleStat[]>();
	let maxLayer = 0;
	for (const module of modules) {
		const l = layerOf.get(module.path) ?? 0;
		maxLayer = Math.max(maxLayer, l);
		const row = rows.get(l);
		if (row) row.push(module);
		else rows.set(l, [module]);
	}

	const rowWidths = new Map<number, number>();
	let canvasWidth = 0;
	for (const [l, row] of rows) {
		row.sort((a, b) => a.path.localeCompare(b.path));
		const width =
			row.reduce((sum, module) => sum + cardWidth(module.path), 0) +
			COL_GAP * (row.length - 1);
		rowWidths.set(l, width);
		canvasWidth = Math.max(canvasWidth, width);
	}

	const placed = new Map<string, PlacedModule>();
	for (const [l, row] of rows) {
		const y = (maxLayer - l) * (CARD_HEIGHT + ROW_GAP);
		let x = (canvasWidth - (rowWidths.get(l) ?? 0)) / 2;
		for (const module of row) {
			const width = cardWidth(module.path);
			placed.set(module.path, { module, x, y, width });
			x += width + COL_GAP;
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
		width: canvasWidth,
		height: maxLayer * (CARD_HEIGHT + ROW_GAP) + CARD_HEIGHT,
		truncated,
	};
}

export function ModuleMap({
	initialSelection,
	onSearchModule,
}: {
	initialSelection?: string;
	onSearchModule: (path: string) => void;
}) {
	const [data, setData] = useState<GraphModules | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [selected, setSelected] = useState<string | null>(
		initialSelection ?? null,
	);

	useEffect(() => {
		const controller = new AbortController();
		fetchGraphModules(controller.signal)
			.then(setData)
			.catch((loadError: unknown) => {
				if (controller.signal.aborted) return;
				setError(
					loadError instanceof Error ? loadError.message : "Load failed",
				);
			});
		return () => controller.abort();
	}, []);

	const layout = useMemo(() => (data ? layoutModules(data) : null), [data]);

	const neighbors = useMemo(() => {
		const set = new Set<string>();
		if (!selected || !layout) return set;
		for (const edge of layout.edges) {
			if (edge.from === selected) set.add(edge.to);
			if (edge.to === selected) set.add(edge.from);
		}
		return set;
	}, [selected, layout]);

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
							className="absolute top-3 left-3 z-10 flex max-w-[75%] items-center gap-2 rounded-md border border-border bg-bg-elevated/95 py-1 pr-1 pl-2.5 text-[11px] shadow-sm"
						>
							<span className="truncate font-mono text-fg">
								{selectedModule.path}
							</span>
							<span className="shrink-0 text-[10px] text-fg-dim tabular-nums">
								{selectedModule.files} files · {selectedModule.nodes} symbols ·{" "}
								<span className="text-info">{dependsOn} deps</span> ·{" "}
								<span className="text-purple">{dependedBy} dependents</span>
							</span>
							<button
								type="button"
								title="Find symbols in this module"
								aria-label="Find symbols in this module"
								onClick={() => onSearchModule(selectedModule.path)}
								className="grid h-5 w-5 shrink-0 place-items-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
							>
								<Search size={11} />
							</button>
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
				{layout.edges.map((edge) => {
					const outgoing = selected === edge.from;
					const incoming = selected === edge.to;
					const color = outgoing
						? "var(--color-info)"
						: incoming
							? "var(--color-purple)"
							: "var(--color-border-strong)";
					return (
						<path
							key={`${edge.from}->${edge.to}`}
							d={`M ${edge.x0} ${edge.y0} C ${edge.x0} ${edge.y0 + ROW_GAP / 2}, ${edge.x1} ${edge.y1 - ROW_GAP / 2}, ${edge.x1} ${edge.y1}`}
							fill="none"
							stroke={color}
							strokeWidth={outgoing || incoming ? 1.75 : 1}
							opacity={selected && !outgoing && !incoming ? 0.25 : 0.9}
						/>
					);
				})}
			</svg>
			{layout.placed.map(({ module, x, y, width }) => {
				const active = selected === module.path;
				const dimmed =
					selected !== null && !active && !neighbors.has(module.path);
				return (
					<div
						key={module.path}
						data-module-card
						role="button"
						tabIndex={0}
						title={`${module.path} · ${module.files} files · ${module.nodes} symbols`}
						onClick={(event) => {
							event.stopPropagation();
							setSelected(active ? null : module.path);
						}}
						onKeyDown={(event) => {
							if (event.key !== "Enter" && event.key !== " ") return;
							event.preventDefault();
							setSelected(active ? null : module.path);
						}}
						className={`absolute cursor-pointer overflow-hidden rounded-lg border px-2 py-1 shadow-sm transition-[opacity,border-color] ${
							active
								? "border-accent bg-bg-elevated"
								: "border-border bg-bg-surface hover:border-border-strong"
						} ${dimmed ? "opacity-35" : ""}`}
						style={{ left: x, top: y, width, height: CARD_HEIGHT }}
					>
						<div className="truncate font-mono text-[11px] text-fg">
							{module.path}
						</div>
						<div className="truncate text-[9px] text-fg-dim tabular-nums">
							{module.files} files · {module.nodes} symbols
						</div>
					</div>
				);
			})}
		</PanZoomCanvas>
	);
}
