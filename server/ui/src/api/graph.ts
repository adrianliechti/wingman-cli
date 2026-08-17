export type GraphNodeKind =
	| "function"
	| "method"
	| "class"
	| "interface"
	| "type"
	| "constructor"
	| "module"
	| "constant"
	| "variable";

export interface GraphNode {
	id: string;
	kind: GraphNodeKind;
	name: string;
	file: string;
	start_line: number;
	end_line: number;
	name_line?: number;
	name_col?: number;
	lang: string;
}

export interface GraphCoverageIssue {
	file: string;
	reason: string;
}

export interface GraphStatus {
	indexed: boolean;
	indexed_at: string;
	stale: boolean;
	files: number;
	nodes: number;
	edges: number;
	skipped?: GraphCoverageIssue[];
}

export interface GraphLangStat {
	lang: string;
	files: number;
	nodes: number;
}

export interface GraphModuleStat {
	path: string;
	files: number;
	nodes: number;
}

export interface GraphModuleDep {
	module: string;
	depends_on: number;
	depended_by: number;
}

export interface GraphHotspot {
	node: GraphNode;
	callers: number;
	callees: number;
}

export interface GraphArch {
	languages: GraphLangStat[] | null;
	total_files: number;
	total_nodes: number;
	total_edges: number;
	layers: GraphModuleStat[] | null;
	modules: GraphModuleStat[] | null;
	module_deps: GraphModuleDep[] | null;
	entry_points: GraphNode[] | null;
	hotspots: GraphHotspot[] | null;
}

export interface GraphOverview {
	status: GraphStatus;
	arch: GraphArch;
}

export interface GraphSearchResult {
	nodes: GraphNode[];
	total: number;
	offset: number;
	has_more: boolean;
}

export interface GraphContentHit {
	node: GraphNode;
	match_lines: number[];
	callers: number;
	callees: number;
	score: number;
}

export interface GraphRawContentHit {
	file: string;
	line: number;
	content: string;
}

export interface GraphContentSearchResult {
	hits: GraphContentHit[];
	raw?: GraphRawContentHit[];
	total_line_hits: number;
	total_results: number;
	total_raw_results: number;
	offset: number;
	has_more: boolean;
	raw_has_more: boolean;
}

export interface GraphNeighborhood {
	node: GraphNode;
	callers: GraphNode[] | null;
	callees: GraphNode[] | null;
	extends?: GraphNode[] | null;
	subtypes?: GraphNode[] | null;
	implements?: GraphNode[] | null;
	implementers?: GraphNode[] | null;
	others?: GraphNode[] | null;
}

export interface GraphModuleEdge {
	from: string;
	to: string;
}

export interface GraphModules {
	modules: GraphModuleStat[];
	edges: GraphModuleEdge[];
}

export interface GraphWeekActivity {
	week: string;
	commits: number;
}

export interface GraphAuthorStat {
	name: string;
	commits: number;
	files: number;
	last: string;
}

export interface GraphChurnStat {
	file: string;
	commits: number;
	authors: number;
}

export interface GraphAuthorSeries {
	name: string;
	weeks: number[];
}

export interface GraphModuleActivity {
	module: string;
	commits: number;
}

export interface GraphInsights {
	commits: number;
	since: string;
	weeks: GraphWeekActivity[];
	author_weeks: GraphAuthorSeries[];
	punch: number[][];
	authors: GraphAuthorStat[];
	modules: GraphModuleActivity[];
	churn: GraphChurnStat[];
}

async function request<T>(input: string, init?: RequestInit): Promise<T> {
	const response = await fetch(input, init);
	if (!response.ok) {
		const detail = (await response.text()).trim();
		throw new Error(detail || `Request failed (${response.status}).`);
	}
	return (await response.json()) as T;
}

export function fetchGraphOverview(signal?: AbortSignal) {
	return request<GraphOverview>("/api/graph/overview", { signal });
}

export function reindexGraph(signal?: AbortSignal) {
	return request<GraphStatus>("/api/graph/index", {
		method: "POST",
		signal,
	});
}

export function fetchGraphModules(signal?: AbortSignal) {
	return request<GraphModules>("/api/graph/modules", { signal });
}

export function fetchGraphInsights(signal?: AbortSignal) {
	return request<GraphInsights>("/api/graph/insights", { signal });
}

export function fetchGraphSummaries(
	modules: string[],
	cachedOnly?: boolean,
	signal?: AbortSignal,
) {
	return request<{ summaries: Record<string, string> }>(
		"/api/graph/summaries",
		{
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ modules, cached_only: cachedOnly ?? false }),
			signal,
		},
	);
}

export function searchGraphSymbols(
	options: {
		query: string;
		kind?: string;
		file?: string;
		limit?: number;
		offset?: number;
	},
	signal?: AbortSignal,
) {
	const params = new URLSearchParams({ q: options.query });
	if (options.kind) params.set("kind", options.kind);
	if (options.file) params.set("file", options.file);
	if (options.limit) params.set("limit", String(options.limit));
	if (options.offset) params.set("offset", String(options.offset));
	return request<GraphSearchResult>(`/api/graph/search?${params}`, { signal });
}

export function searchGraphContent(
	options: {
		pattern: string;
		regex?: boolean;
		ignore_case?: boolean;
		file?: string;
		glob?: string;
		limit?: number;
	},
	signal?: AbortSignal,
) {
	return request<GraphContentSearchResult>("/api/graph/content-search", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(options),
		signal,
	});
}

export function fetchGraphSymbol(
	options: { id?: string; name?: string; file?: string },
	signal?: AbortSignal,
) {
	const params = new URLSearchParams();
	if (options.id) params.set("id", options.id);
	if (options.name) params.set("name", options.name);
	if (options.file) params.set("file", options.file);
	return request<GraphNeighborhood>(`/api/graph/symbol?${params}`, { signal });
}
