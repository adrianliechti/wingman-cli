import { AlertTriangle, Loader2, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import {
	type GraphNode,
	type GraphOverview,
	fetchGraphOverview,
	reindexGraph,
} from "../../api/insights";
import { ActivityView } from "./ActivityView";
import { ModuleMap } from "./ModuleMap";
import { OverviewView } from "./OverviewView";
import { SearchView } from "./SearchView";
import { SymbolView } from "./SymbolView";

type GraphView = "overview" | "map" | "search" | "activity";

interface Focus {
	id?: string;
	name?: string;
	file?: string;
}

interface InsightsTabMemory {
	view: GraphView;
	focus: Focus | null;
	history: Focus[];
	overview?: GraphOverview;
	mapSelection?: string;
	searchSeed?: { query?: string; file?: string };
}

const memory: InsightsTabMemory = {
	view: "overview",
	focus: null,
	history: [],
};

const VIEWS: { id: GraphView; label: string }[] = [
	{ id: "overview", label: "Overview" },
	{ id: "map", label: "Architecture" },
	{ id: "search", label: "Code" },
	{ id: "activity", label: "Authors" },
];

export function InsightsTab({
	onOpenFile,
}: {
	onOpenFile: (path: string, line?: number, column?: number) => void;
}) {
	const [view, setView] = useState<GraphView>(memory.view);
	const [focus, setFocus] = useState<Focus | null>(memory.focus);
	const [overview, setOverview] = useState<GraphOverview | null>(
		memory.overview ?? null,
	);
	const [loading, setLoading] = useState(!memory.overview);
	const [indexing, setIndexing] = useState(false);
	const [indexRevision, setIndexRevision] = useState(0);
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
			memory.overview = result;
			setOverview(result);
			if (reindex) setIndexRevision((value) => value + 1);
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
		setView("search");
	}, []);

	const goBack = useCallback(() => {
		const previous = memory.history[memory.history.length - 1];
		if (previous) {
			memory.history = memory.history.slice(0, -1);
			setFocus(previous);
			return;
		}
		setFocus(null);
	}, []);

	const selectModule = useCallback((path: string) => {
		memory.mapSelection = path;
		setView("map");
	}, []);

	const searchModule = useCallback((path: string) => {
		memory.searchSeed = { file: path };
		memory.history = [];
		setFocus(null);
		setView("search");
	}, []);

	const status = overview?.status;
	const refreshTitle = [
		"Re-index codebase",
		status?.indexed_at
			? `Last indexed ${new Date(status.indexed_at).toLocaleString()}`
			: "",
		status?.stale ? "Source files changed since this index was built." : "",
		...(status?.skipped ?? [])
			.slice(0, 8)
			.map((issue) => `${issue.file}: ${issue.reason}`),
	]
		.filter(Boolean)
		.join("\n");

	return (
		<div className="flex h-full min-h-0 flex-col overflow-hidden bg-bg">
			<div
				role="tablist"
				aria-label="Insights views"
				className="flex h-10 shrink-0 items-center gap-1 border-b border-border-subtle bg-bg-surface/20 px-2"
			>
				{VIEWS.map((entry) => (
					<button
						key={entry.id}
						type="button"
						role="tab"
						aria-selected={view === entry.id}
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
				<button
					type="button"
					disabled={indexing || loading}
					onClick={() => void load(true)}
					title={refreshTitle}
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
			{error && overview && (
				<div className="flex shrink-0 items-center gap-1.5 border-b border-danger/20 bg-danger/5 px-3 py-1.5 text-[10px] text-danger/90">
					<AlertTriangle size={11} className="shrink-0" />
					<span className="min-w-0 flex-1 truncate">Could not refresh insights: {error}</span>
					<button type="button" onClick={() => void load(false)} className="shrink-0 text-fg-muted hover:text-fg">
						Try again
					</button>
				</div>
			)}
			<div className="min-h-0 flex-1">
				{view === "overview" ? (
					loading && !overview ? (
						<div className="flex h-full items-center justify-center gap-1.5 text-[11px] text-fg-dim">
							<Loader2 size={11} className="animate-spin" />
							<span>
								Indexing codebase…
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
						refreshKey={indexRevision}
						initialSelection={memory.mapSelection}
						onSearchModule={searchModule}
					/>
				) : view === "activity" ? (
					<ActivityView
						refreshKey={indexRevision}
						onOpenFile={onOpenFile}
					/>
				) : (
					<div className="h-full">
						<div className={focus ? "hidden" : "h-full"}>
							<SearchView
								refreshKey={indexRevision}
								seed={memory.searchSeed}
								onExplore={explore}
								onOpenFile={onOpenFile}
							/>
						</div>
						{focus && (
							<SymbolView
								refreshKey={indexRevision}
								focus={focus}
								canGoBack
								onBack={goBack}
								onExplore={explore}
								onOpenFile={onOpenFile}
							/>
						)}
					</div>
				)}
			</div>
		</div>
	);
}
