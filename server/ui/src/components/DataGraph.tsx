import {
	Check,
	ChevronRight,
	Copy,
	Maximize,
	ZoomIn,
	ZoomOut,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	collectionEntries,
	collectionSummary,
	formatScalar,
	scalarClass,
} from "../utils/dataValue";

const MAX_GRAPH_NODES = 1_200;
const MAX_NODE_ROWS = 50;
const MAX_VALUE_CHARS = 60;
const ROW_HEIGHT = 22;
const NODE_PADDING_Y = 4;
const CHAR_WIDTH = 6.6;
const MIN_NODE_WIDTH = 110;
const MAX_NODE_WIDTH = 340;
const GAP_X = 64;
const GAP_Y = 14;
const MIN_ZOOM = 0.15;
const MAX_ZOOM = 2.5;

interface ScalarRow {
	kind: "scalar";
	key?: string;
	text: string;
	className: string;
}

interface ChildRow {
	kind: "child";
	key: string;
	badge: string;
	child: GraphNode;
}

type GraphRow = ScalarRow | ChildRow;

interface GraphNode {
	id: string;
	path: string;
	rows: GraphRow[];
	hiddenRows: number;
	width: number;
	height: number;
}

interface PlacedNode {
	node: GraphNode;
	x: number;
	y: number;
}

interface GraphEdge {
	id: string;
	childId: string;
	x0: number;
	y0: number;
	x1: number;
	y1: number;
}

const IDENTIFIER_KEY = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

function childPath(path: string, key: string, isArray: boolean) {
	if (isArray) return `${path}[${key}]`;
	if (IDENTIFIER_KEY.test(key)) {
		return path === "$" ? `$.${key}` : `${path}.${key}`;
	}
	return `${path}[${JSON.stringify(key)}]`;
}

function buildGraph(value: unknown) {
	const budget = { nodes: 0, truncated: false };
	const root = buildNode(value, "$", "$", [], budget);
	return { root, truncated: budget.truncated };
}

function buildNode(
	value: unknown,
	id: string,
	path: string,
	ancestors: object[],
	budget: { nodes: number; truncated: boolean },
): GraphNode {
	budget.nodes += 1;
	const entries = collectionEntries(value);
	const rows: GraphRow[] = [];
	let hiddenRows = 0;

	if (entries.length === 0) {
		rows.push({
			kind: "scalar",
			text: formatScalar(value),
			className: scalarClass(value),
		});
	} else {
		const isArray = Array.isArray(value);
		const nextAncestors = [...ancestors, value as object];
		for (const [key, child] of entries) {
			if (rows.length >= MAX_NODE_ROWS) {
				hiddenRows = entries.length - rows.length;
				break;
			}
			if (child !== null && typeof child === "object") {
				if (ancestors.includes(child)) {
					rows.push({
						kind: "scalar",
						key,
						text: "[Circular]",
						className: "text-fg-dim",
					});
					continue;
				}
				const childEntries = collectionEntries(child);
				if (childEntries.length > 0) {
					if (budget.nodes >= MAX_GRAPH_NODES) {
						budget.truncated = true;
						rows.push({
							kind: "scalar",
							key,
							text: collectionSummary(child),
							className: "text-fg-dim",
						});
						continue;
					}
					rows.push({
						kind: "child",
						key,
						badge: Array.isArray(child)
							? `[${childEntries.length}]`
							: `{${childEntries.length}}`,
						child: buildNode(
							child,
							`${id}/${encodeURIComponent(key)}`,
							childPath(path, key, isArray),
							nextAncestors,
							budget,
						),
					});
					continue;
				}
			}
			rows.push({
				kind: "scalar",
				key,
				text: formatScalar(child),
				className: scalarClass(child),
			});
		}
	}

	let maxChars = 4;
	let hasChild = false;
	for (const row of rows) {
		if (row.kind === "scalar") {
			const keyChars = row.key === undefined ? 0 : row.key.length + 2;
			maxChars = Math.max(
				maxChars,
				keyChars + Math.min(row.text.length, MAX_VALUE_CHARS),
			);
		} else {
			hasChild = true;
			maxChars = Math.max(maxChars, row.key.length + row.badge.length + 4);
		}
	}
	const width = Math.round(
		Math.min(
			MAX_NODE_WIDTH,
			Math.max(
				MIN_NODE_WIDTH,
				maxChars * CHAR_WIDTH + 20 + (hasChild ? 18 : 0),
			),
		),
	);
	const height =
		NODE_PADDING_Y * 2 + (rows.length + (hiddenRows > 0 ? 1 : 0)) * ROW_HEIGHT;
	return { id, path, rows, hiddenRows, width, height };
}

function layoutGraph(root: GraphNode, collapsed: ReadonlySet<string>) {
	const subtrees = new Map<string, number>();
	const visibleChildRows = (node: GraphNode) =>
		node.rows.filter(
			(row): row is ChildRow =>
				row.kind === "child" && !collapsed.has(row.child.id),
		);

	const measure = (node: GraphNode): number => {
		const children = visibleChildRows(node);
		let total = 0;
		for (const row of children) total += measure(row.child) + GAP_Y;
		if (children.length > 0) total -= GAP_Y;
		const subtree = Math.max(node.height, total);
		subtrees.set(node.id, subtree);
		return subtree;
	};
	measure(root);

	const placed: PlacedNode[] = [];
	const edges: GraphEdge[] = [];
	let maxX = 0;

	const place = (node: GraphNode, x: number, top: number) => {
		const subtree = subtrees.get(node.id) ?? node.height;
		const y = top + (subtree - node.height) / 2;
		placed.push({ node, x, y });
		maxX = Math.max(maxX, x + node.width);

		const children = visibleChildRows(node);
		if (children.length === 0) return;
		let childrenTotal = 0;
		for (const row of children) {
			childrenTotal += (subtrees.get(row.child.id) ?? row.child.height) + GAP_Y;
		}
		childrenTotal -= GAP_Y;

		const childX = x + node.width + GAP_X;
		let cursor = top + (subtree - childrenTotal) / 2;
		for (const row of children) {
			const childSubtree = subtrees.get(row.child.id) ?? row.child.height;
			const rowIndex = node.rows.indexOf(row);
			edges.push({
				id: `${node.id}->${row.child.id}`,
				childId: row.child.id,
				x0: x + node.width,
				y0: y + NODE_PADDING_Y + rowIndex * ROW_HEIGHT + ROW_HEIGHT / 2,
				x1: childX,
				y1: cursor + childSubtree / 2,
			});
			place(row.child, childX, cursor);
			cursor += childSubtree + GAP_Y;
		}
	};
	place(root, 0, 0);

	return {
		placed,
		edges,
		width: maxX,
		height: subtrees.get(root.id) ?? root.height,
	};
}

function clampZoom(k: number) {
	return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));
}

export function DataGraph({ value }: { value: unknown }) {
	const containerRef = useRef<HTMLDivElement | null>(null);
	const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
	const [selected, setSelected] = useState<string | null>(null);
	const [transform, setTransform] = useState({ x: 48, y: 48, k: 1 });
	const [dragging, setDragging] = useState(false);
	const [copied, setCopied] = useState(false);
	const transformRef = useRef(transform);
	transformRef.current = transform;
	const dragRef = useRef<{
		pointerId: number;
		startX: number;
		startY: number;
		originX: number;
		originY: number;
		moved: boolean;
	} | null>(null);

	const graph = useMemo(() => buildGraph(value), [value]);
	const layout = useMemo(
		() => layoutGraph(graph.root, collapsed),
		[graph, collapsed],
	);
	const layoutRef = useRef(layout);
	layoutRef.current = layout;

	const fit = useCallback(() => {
		const container = containerRef.current;
		if (!container) return;
		const { width, height } = layoutRef.current;
		const cw = container.clientWidth;
		const ch = container.clientHeight;
		if (!cw || !ch || !width || !height) return;
		const k = clampZoom(Math.min((cw - 96) / width, (ch - 96) / height, 1));
		setTransform({
			k,
			x: (cw - width * k) / 2,
			y: (ch - height * k) / 2,
		});
	}, []);

	const fittedFor = useRef<unknown>(null);
	useEffect(() => {
		if (fittedFor.current === graph) return;
		fittedFor.current = graph;
		setCollapsed(new Set());
		setSelected(null);
		fit();
	}, [graph, fit]);

	useEffect(() => {
		const container = containerRef.current;
		if (!container) return;
		const onWheel = (event: WheelEvent) => {
			event.preventDefault();
			const current = transformRef.current;
			if (event.ctrlKey || event.metaKey) {
				const rect = container.getBoundingClientRect();
				const px = event.clientX - rect.left;
				const py = event.clientY - rect.top;
				const k = clampZoom(current.k * Math.exp(-event.deltaY * 0.01));
				const scale = k / current.k;
				setTransform({
					k,
					x: px - (px - current.x) * scale,
					y: py - (py - current.y) * scale,
				});
			} else {
				setTransform({
					...current,
					x: current.x - event.deltaX,
					y: current.y - event.deltaY,
				});
			}
		};
		container.addEventListener("wheel", onWheel, { passive: false });
		return () => container.removeEventListener("wheel", onWheel);
	}, []);

	const zoomBy = useCallback((factor: number) => {
		const container = containerRef.current;
		if (!container) return;
		const current = transformRef.current;
		const k = clampZoom(current.k * factor);
		const scale = k / current.k;
		const px = container.clientWidth / 2;
		const py = container.clientHeight / 2;
		setTransform({
			k,
			x: px - (px - current.x) * scale,
			y: py - (py - current.y) * scale,
		});
	}, []);

	const toggleCollapse = useCallback((id: string) => {
		setCollapsed((previous) => {
			const next = new Set(previous);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});
	}, []);

	const selectedNode = selected
		? layout.placed.find((entry) => entry.node.id === selected)?.node
		: undefined;

	const copyPath = async () => {
		if (!selectedNode) return;
		await navigator.clipboard.writeText(selectedNode.path);
		setCopied(true);
		window.setTimeout(() => setCopied(false), 1200);
	};

	return (
		<div
			ref={containerRef}
			data-graph-preview
			className="relative h-full touch-none overflow-hidden bg-bg"
			style={{
				cursor: dragging ? "grabbing" : "grab",
				backgroundImage:
					"radial-gradient(var(--color-border-subtle) 1px, transparent 1px)",
				backgroundSize: `${24 * transform.k}px ${24 * transform.k}px`,
				backgroundPosition: `${transform.x}px ${transform.y}px`,
			}}
			onPointerDown={(event) => {
				if (event.button !== 0) return;
				const target = event.target as Element;
				if (target.closest("[data-graph-node],[data-graph-hud],button")) {
					return;
				}
				const current = transformRef.current;
				dragRef.current = {
					pointerId: event.pointerId,
					startX: event.clientX,
					startY: event.clientY,
					originX: current.x,
					originY: current.y,
					moved: false,
				};
				event.currentTarget.setPointerCapture(event.pointerId);
				setDragging(true);
			}}
			onPointerMove={(event) => {
				const drag = dragRef.current;
				if (!drag || drag.pointerId !== event.pointerId) return;
				const dx = event.clientX - drag.startX;
				const dy = event.clientY - drag.startY;
				if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true;
				setTransform({
					...transformRef.current,
					x: drag.originX + dx,
					y: drag.originY + dy,
				});
			}}
			onPointerUp={(event) => {
				const drag = dragRef.current;
				if (!drag || drag.pointerId !== event.pointerId) return;
				dragRef.current = null;
				setDragging(false);
				if (!drag.moved) setSelected(null);
			}}
			onPointerCancel={() => {
				dragRef.current = null;
				setDragging(false);
			}}
		>
			<div
				className="absolute top-0 left-0"
				style={{
					width: layout.width,
					height: layout.height,
					transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.k})`,
					transformOrigin: "0 0",
				}}
			>
				<svg
					aria-hidden="true"
					className="pointer-events-none absolute top-0 left-0 overflow-visible"
					width={Math.max(1, layout.width)}
					height={Math.max(1, layout.height)}
				>
					{layout.edges.map((edge) => {
						const active =
							selected !== null &&
							(selected === edge.childId ||
								selected.startsWith(`${edge.childId}/`));
						const mx = (edge.x0 + edge.x1) / 2;
						const color = active
							? "var(--color-accent)"
							: "var(--color-border-strong)";
						return (
							<g key={edge.id}>
								<path
									d={`M ${edge.x0} ${edge.y0} C ${mx} ${edge.y0}, ${mx} ${edge.y1}, ${edge.x1} ${edge.y1}`}
									fill="none"
									stroke={color}
									strokeWidth={active ? 1.75 : 1.25}
								/>
								<circle cx={edge.x0} cy={edge.y0} r={2} fill={color} />
								<circle cx={edge.x1} cy={edge.y1} r={2} fill={color} />
							</g>
						);
					})}
				</svg>
				{layout.placed.map(({ node, x, y }) => (
					<GraphNodeCard
						key={node.id}
						node={node}
						x={x}
						y={y}
						selected={selected === node.id}
						collapsed={collapsed}
						onSelect={setSelected}
						onToggle={toggleCollapse}
					/>
				))}
			</div>

			{selectedNode && (
				<div
					data-graph-hud
					className="absolute top-3 left-3 z-10 flex max-w-[70%] items-center gap-1 rounded-md border border-border bg-bg-elevated/95 py-1 pr-1 pl-2.5 font-mono text-[11px] text-fg-muted shadow-sm"
				>
					<span className="truncate">{selectedNode.path}</span>
					<button
						type="button"
						aria-label="Copy path"
						title="Copy path"
						onClick={() => void copyPath()}
						className="grid h-5 w-5 shrink-0 place-items-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
					>
						{copied ? (
							<Check size={11} className="text-success" />
						) : (
							<Copy size={11} />
						)}
					</button>
				</div>
			)}

			<div
				data-graph-hud
				className="absolute right-3 bottom-3 z-10 flex items-center gap-0.5 rounded-md border border-border bg-bg-elevated/95 p-0.5 shadow-sm"
			>
				<button
					type="button"
					aria-label="Zoom out"
					title="Zoom out"
					onClick={() => zoomBy(1 / 1.25)}
					className="grid h-6 w-6 place-items-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
				>
					<ZoomOut size={13} />
				</button>
				<button
					type="button"
					title="Reset zoom"
					onClick={() => zoomBy(1 / transform.k)}
					className="h-6 min-w-11 rounded px-1 text-center text-[11px] text-fg-muted tabular-nums hover:bg-bg-hover hover:text-fg"
				>
					{Math.round(transform.k * 100)}%
				</button>
				<button
					type="button"
					aria-label="Zoom in"
					title="Zoom in"
					onClick={() => zoomBy(1.25)}
					className="grid h-6 w-6 place-items-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
				>
					<ZoomIn size={13} />
				</button>
				<div className="mx-0.5 h-4 w-px bg-border" />
				<button
					type="button"
					aria-label="Fit to view"
					title="Fit to view"
					onClick={fit}
					className="grid h-6 w-6 place-items-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
				>
					<Maximize size={13} />
				</button>
			</div>

			{graph.truncated && (
				<div
					data-graph-hud
					className="absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-md border border-warning/30 bg-bg-elevated/95 px-3 py-1.5 text-[11px] text-warning shadow-sm"
				>
					Large document — graph limited to {MAX_GRAPH_NODES.toLocaleString()}{" "}
					nodes. Switch to Tree view for the full data.
				</div>
			)}
		</div>
	);
}

function GraphNodeCard({
	node,
	x,
	y,
	selected,
	collapsed,
	onSelect,
	onToggle,
}: {
	node: GraphNode;
	x: number;
	y: number;
	selected: boolean;
	collapsed: ReadonlySet<string>;
	onSelect: (id: string) => void;
	onToggle: (id: string) => void;
}) {
	return (
		<div
			data-graph-node
			role="button"
			tabIndex={0}
			onClick={(event) => {
				event.stopPropagation();
				onSelect(node.id);
			}}
			onKeyDown={(event) => {
				if (event.key !== "Enter" && event.key !== " ") return;
				event.preventDefault();
				onSelect(node.id);
			}}
			className={`absolute rounded-lg border font-mono text-[11px] shadow-sm transition-colors select-text ${
				selected
					? "border-accent bg-bg-elevated"
					: "border-border bg-bg-surface hover:border-border-strong"
			}`}
			style={{
				left: x,
				top: y,
				width: node.width,
				height: node.height,
				paddingBlock: NODE_PADDING_Y,
			}}
		>
			{node.rows.map((row, index) =>
				row.kind === "scalar" ? (
					<div
						key={`${row.key ?? ""}:${index}`}
						className="flex h-[22px] items-center gap-1.5 overflow-hidden px-2.5"
					>
						{row.key !== undefined && (
							<span className="shrink-0 text-accent">{row.key}:</span>
						)}
						<span className={`truncate ${row.className}`} title={row.text}>
							{row.text}
						</span>
					</div>
				) : (
					<button
						key={`${row.key}:${index}`}
						type="button"
						aria-expanded={!collapsed.has(row.child.id)}
						title={
							collapsed.has(row.child.id)
								? `Expand ${row.key}`
								: `Collapse ${row.key}`
						}
						onClick={(event) => {
							event.stopPropagation();
							onToggle(row.child.id);
							onSelect(node.id);
						}}
						className="flex h-[22px] w-full items-center gap-1.5 rounded-sm px-2.5 text-left hover:bg-bg-hover"
					>
						<span className="truncate text-accent">{row.key}:</span>
						<span className="ml-auto shrink-0 rounded bg-bg-active px-1 text-[10px] leading-4 text-fg-muted">
							{row.badge}
						</span>
						<ChevronRight
							size={11}
							className={`shrink-0 text-fg-dim transition-transform ${
								collapsed.has(row.child.id) ? "" : "rotate-90"
							}`}
						/>
					</button>
				),
			)}
			{node.hiddenRows > 0 && (
				<div className="flex h-[22px] items-center px-2.5 text-fg-dim">
					… {node.hiddenRows} more
				</div>
			)}
		</div>
	);
}
