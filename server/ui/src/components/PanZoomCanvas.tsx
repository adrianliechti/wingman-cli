import { Maximize, ZoomIn, ZoomOut } from "lucide-react";
import {
	type ReactNode,
	useCallback,
	useEffect,
	useLayoutEffect,
	useRef,
	useState,
} from "react";

const MIN_ZOOM = 0.15;
const MAX_ZOOM = 2.5;
const FIT_PADDING = 96;
const UNFIT = Symbol("unfit");

function clampZoom(k: number) {
	return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));
}

export function PanZoomCanvas({
	width,
	height,
	fitKey,
	wheelMode = "pan-and-zoom",
	dragExclude,
	onBackgroundClick,
	children,
	hud,
}: {
	width: number;
	height: number;
	fitKey?: unknown;
	wheelMode?: "pan-and-zoom" | "zoom-only";
	dragExclude?: string;
	onBackgroundClick?: () => void;
	children: ReactNode;
	hud?: ReactNode;
}) {
	const containerRef = useRef<HTMLDivElement | null>(null);
	const [transform, setTransform] = useState({ x: 48, y: 48, k: 1 });
	const [dragging, setDragging] = useState(false);
	const transformRef = useRef(transform);
	const contentRef = useRef({ width, height });
	useLayoutEffect(() => {
		transformRef.current = transform;
		contentRef.current = { width, height };
	}, [transform, width, height]);
	const dragRef = useRef<{
		pointerId: number;
		startX: number;
		startY: number;
		originX: number;
		originY: number;
		moved: boolean;
	} | null>(null);

	const fit = useCallback(() => {
		const container = containerRef.current;
		if (!container) return;
		const { width: w, height: h } = contentRef.current;
		const cw = container.clientWidth;
		const ch = container.clientHeight;
		if (!cw || !ch || !w || !h) return;
		const k = clampZoom(
			Math.min((cw - FIT_PADDING) / w, (ch - FIT_PADDING) / h, 1),
		);
		setTransform({
			k,
			x: (cw - w * k) / 2,
			y: (ch - h * k) / 2,
		});
	}, []);

	const fittedFor = useRef<unknown>(UNFIT);
	useEffect(() => {
		if (fittedFor.current === fitKey) return;
		fittedFor.current = fitKey;
		fit();
	}, [fitKey, fit]);

	useEffect(() => {
		const container = containerRef.current;
		if (!container) return;
		const onWheel = (event: WheelEvent) => {
			const zooming = event.ctrlKey || event.metaKey;
			if (!zooming && wheelMode === "zoom-only") return;
			event.preventDefault();
			const current = transformRef.current;
			if (zooming) {
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
	}, [wheelMode]);

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

	return (
		<div
			ref={containerRef}
			data-pan-zoom-preview
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
				const excluded = dragExclude
					? `[data-canvas-hud],button,${dragExclude}`
					: "[data-canvas-hud],button";
				if (target.closest(excluded)) return;
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
				if (!drag.moved) onBackgroundClick?.();
			}}
			onPointerCancel={() => {
				dragRef.current = null;
				setDragging(false);
			}}
		>
			<div
				className="absolute top-0 left-0"
				style={{
					width,
					height,
					transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.k})`,
					transformOrigin: "0 0",
				}}
			>
				{children}
			</div>
			{hud}
			<div
				data-canvas-hud
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
		</div>
	);
}
