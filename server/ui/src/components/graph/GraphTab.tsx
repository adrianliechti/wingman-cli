import { Loader2, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import {
	type GraphNode,
	type GraphOverview,
	fetchGraphOverview,
	reindexGraph,
} from "../../api/graph";
import { ModuleMap } from "./ModuleMap";
import { OverviewView } from "./OverviewView";
import { SearchView } from "./SearchView";
import { SymbolView } from "./SymbolView";

type GraphView = "overview" | "map" | "search" | "symbol";

interface Focus {
	id?: string;
	name?: string;
	file?: string;
}

interface GraphTabMemory {
	view: GraphView;
	focus: Focus | null;
	history: Focus[];
	mapSelection?: string;
	searchSeed?: { query?: string; file?: string };
}

const memory: GraphTabMemory = {
	view: "overview",
	focus: null,
	history: [],
};

const VIEWS: { id: GraphView; label: string }[] = [
	{ id: "overview", label: "Overview" },
	{ id: "map", label: "Modules" },
	{ id: "search", label: "Search" },
	{ id: "symbol", label: "Symbol" },
];

export function GraphTab({
	onOpenFile,
}: {
	onOpenFile: (path: string, line?: number, column?: number) => void;
}) {
	const [view, setView] = useState<GraphView>(memory.view);
	const [focus, setFocus] = useState<Focus | null>(memory.focus);
	const [overview, setOverview] = useState<GraphOverview | null>(null);
	const [loading, setLoading] = useState(true);
	const [indexing, setIndexing] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const requestRef = useRef<AbortController | null>(null);

	useEffect(() => {
		memory.view = view;
	}, [view]);
	useEffect(() => {
		memory.focus = focus;
	}, [focus]);

	const load = useCallback(async (reindex: boolean) => {
		requestRef.current?.abort();
		const controller = new AbortController();
		requestRef.current = controller;
		if (reindex) setIndexing(true);
		setError(null);
		try {
			if (reindex) await reindexGraph(controller.signal);
			const result = await fetchGraphOverview(controller.signal);
			if (controller.signal.aborted) return;
			setOverview(result);
		} catch (loadError) {
			if (controller.signal.aborted) return;
			setError(loadError instanceof Error ? loadError.message : "Load failed");
		} finally {
			if (requestRef.current === controller) {
				requestRef.current = null;
				setLoading(false);
				setIndexing(false);
			}
		}
	}, []);

	useEffect(() => {
		void load(false);
		return () => requestRef.current?.abort();
	}, [load]);

	const explore = useCallback((node: GraphNode) => {
		setFocus((previous) => {
			if (previous) {
				memory.history = [...memory.history.slice(-24), previous];
			}
			return { id: node.id, name: node.name, file: node.file };
		});
		setView("symbol");
	}, []);

	const goBack = useCallback(() => {
		const previous = memory.history[memory.history.length - 1];
		if (!previous) return;
		memory.history = memory.history.slice(0, -1);
		setFocus(previous);
	}, []);

	const selectModule = useCallback((path: string) => {
		memory.mapSelection = path;
		setView("map");
	}, []);

	const searchModule = useCallback((path: string) => {
		memory.searchSeed = { file: path };
		setView("search");
	}, []);

	const status = overview?.status;
	const statusText = status?.indexed
		? `${status.files.toLocaleString()} files · ${status.nodes.toLocaleString()} symbols${status.stale ? " · stale" : ""}`
		: "";

	return (
		<div className="flex h-full min-h-0 flex-col overflow-hidden bg-bg">
			<div className="flex h-9 shrink-0 items-center gap-1 border-b border-border-subtle bg-bg-surface/20 px-2">
				{VIEWS.map((entry) => (
					<button
						key={entry.id}
						type="button"
						onClick={() => setView(entry.id)}
						className={`rounded px-2 py-1 text-[11px] transition-colors ${
							view === entry.id
								? "bg-bg-active text-fg"
								: "text-fg-dim hover:bg-bg-hover hover:text-fg-muted"
						}`}
					>
						{entry.label}
					</button>
				))}
				<div className="flex-1" />
				{statusText && (
					<span
						className="truncate text-[10px] text-fg-dim tabular-nums"
						title={
							status?.indexed_at
								? `Indexed ${new Date(status.indexed_at).toLocaleString()}`
								: undefined
						}
					>
						{statusText}
					</span>
				)}
				<button
					type="button"
					disabled={indexing || loading}
					onClick={() => void load(true)}
					title="Re-index codebase"
					aria-label="Re-index codebase"
					className="flex h-6 w-6 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg disabled:opacity-50"
				>
					{indexing ? (
						<Loader2 size={11} className="animate-spin" />
					) : (
						<RefreshCw size={11} />
					)}
				</button>
			</div>
			<div className="min-h-0 flex-1">
				{view === "overview" ? (
					loading || indexing ? (
						<div className="flex h-full items-center justify-center gap-1.5 text-[11px] text-fg-dim">
							<Loader2 size={11} className="animate-spin" />
							<span>
								{indexing ? "Re-indexing codebase…" : "Indexing codebase…"}
							</span>
						</div>
					) : error ? (
						<div className="grid h-full place-items-center px-6 text-center text-[11px] text-danger/80">
							{error}
						</div>
					) : overview ? (
						<OverviewView
							overview={overview}
							onOpenFile={onOpenFile}
							onExplore={explore}
							onSelectModule={selectModule}
						/>
					) : null
				) : view === "map" ? (
					<ModuleMap
						initialSelection={memory.mapSelection}
						onSearchModule={searchModule}
					/>
				) : view === "search" ? (
					<SearchView
						seed={memory.searchSeed}
						onExplore={explore}
						onOpenFile={onOpenFile}
					/>
				) : (
					<SymbolView
						focus={focus}
						canGoBack={memory.history.length > 0}
						onBack={goBack}
						onExplore={explore}
						onOpenFile={onOpenFile}
					/>
				)}
			</div>
		</div>
	);
}
