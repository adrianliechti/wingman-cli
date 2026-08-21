import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	AlertTriangle,
	ChevronDown,
	GitPullRequest,
	Loader2,
	Network,
	RefreshCw,
	ShieldCheck,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import {
	type GraphNode,
	fetchGraphOverview,
	reindexGraph,
} from "../../api/insights";
import { queryKeys } from "../../api/query";
import { ActivityView } from "./ActivityView";
import { FloatingMenu } from "../ui/Floating";
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

const ANALYSES = [
	{
		label: "Review working tree",
		detail: "Find high-confidence issues in current changes",
		command: "/code-review",
		icon: GitPullRequest,
	},
	{
		label: "Security review",
		detail: "Audit trust boundaries and exploitable risks",
		command: "/security-review",
		icon: ShieldCheck,
	},
	{
		label: "Architecture diagram",
		detail: "Generate an evidence-based C4 Mermaid view",
		command:
			"/architecture Generate an evidence-based C4 Mermaid diagram of this repository.",
		icon: Network,
	},
] as const;

export function InsightsTab({
	onOpenFile,
	onStartAnalysis,
}: {
	onOpenFile: (path: string, line?: number, column?: number) => void;
	onStartAnalysis: (command: string) => void;
}) {
	const [view, setView] = useState<GraphView>(memory.view);
	const [focus, setFocus] = useState<Focus | null>(memory.focus);
	const analysisButtonRef = useRef<HTMLButtonElement | null>(null);
	const [analysisOpen, setAnalysisOpen] = useState(false);
	const queryClient = useQueryClient();
	const overviewQuery = useQuery({
		queryKey: queryKeys.insights.overview,
		queryFn: ({ signal }) => fetchGraphOverview(signal),
	});
	const reindexMutation = useMutation({
		mutationFn: () => reindexGraph(),
		onSuccess: () =>
			queryClient.invalidateQueries({ queryKey: queryKeys.insights.all }),
	});
	const overview = overviewQuery.data ?? null;
	const loading = overviewQuery.isPending;
	const indexing = reindexMutation.isPending;
	const queryError = reindexMutation.error ?? overviewQuery.error;
	const error = queryError
		? queryError instanceof Error
			? queryError.message
			: "Load failed"
		: null;

	useEffect(() => {
		memory.view = view;
	}, [view]);
	useEffect(() => {
		memory.focus = focus;
	}, [focus]);

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
					ref={analysisButtonRef}
					type="button"
					onClick={() => setAnalysisOpen((open) => !open)}
					aria-haspopup="menu"
					aria-expanded={analysisOpen}
					className="flex h-6 items-center gap-1 rounded px-1.5 text-[10px] text-fg-dim hover:bg-bg-hover hover:text-fg"
				>
					Analyze <ChevronDown size={10} />
				</button>
				<button
					type="button"
					disabled={indexing || loading}
					onClick={() => reindexMutation.mutate()}
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
			<FloatingMenu
				open={analysisOpen}
				onOpenChange={setAnalysisOpen}
				reference={analysisButtonRef.current}
				placement="bottom-end"
				label="Insights analyses"
				className="z-[100] w-72 rounded-md border border-border bg-bg-elevated/95 py-1 shadow-xl backdrop-blur-sm"
			>
				{ANALYSES.map((analysis) => {
					const Icon = analysis.icon;
					return (
						<button
							key={analysis.command}
							type="button"
							role="menuitem"
							onClick={() => {
								setAnalysisOpen(false);
								onStartAnalysis(analysis.command);
							}}
							className="flex w-full items-start gap-2 px-3 py-2 text-left text-fg-muted hover:bg-bg-hover hover:text-fg"
						>
							<Icon size={13} className="mt-0.5 shrink-0 text-fg-dim" />
							<span className="min-w-0">
								<span className="block text-[11px]">{analysis.label}</span>
								<span className="mt-0.5 block text-[10px] text-fg-dim">
									{analysis.detail}
								</span>
							</span>
						</button>
					);
				})}
			</FloatingMenu>
			{error && overview && (
				<div className="flex shrink-0 items-center gap-1.5 border-b border-danger/20 bg-danger/5 px-3 py-1.5 text-[10px] text-danger/90">
					<AlertTriangle size={11} className="shrink-0" />
					<span className="min-w-0 flex-1 truncate">
						Could not refresh insights: {error}
					</span>
					<button
						type="button"
						onClick={() => {
							reindexMutation.reset();
							void overviewQuery.refetch();
						}}
						className="shrink-0 text-fg-muted hover:text-fg"
					>
						Try again
					</button>
				</div>
			)}
			<div className="min-h-0 flex-1">
				{view === "overview" ? (
					loading && !overview ? (
						<div className="flex h-full items-center justify-center gap-1.5 text-[11px] text-fg-dim">
							<Loader2 size={11} className="animate-spin" />
							<span>Indexing codebase…</span>
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
				) : view === "activity" ? (
					<ActivityView onOpenFile={onOpenFile} />
				) : (
					<div className="h-full">
						<div className={focus ? "hidden" : "h-full"}>
							<SearchView
								seed={memory.searchSeed}
								onExplore={explore}
								onOpenFile={onOpenFile}
							/>
						</div>
						{focus && (
							<SymbolView
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
