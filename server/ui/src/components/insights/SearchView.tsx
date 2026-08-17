import { useVirtualizer } from "@tanstack/react-virtual";
import { CaseSensitive, Loader2, Regex, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	type GraphContentHit,
	type GraphContentSearchResult,
	type GraphNode,
	type GraphRawContentHit,
	type GraphSearchResult,
	searchGraphContent,
	searchGraphSymbols,
} from "../../api/insights";
import { nodeTargetLine } from "./nodes";
import { KindBadge, NodeRow } from "./shared";

const SYMBOL_KINDS = [
	"function",
	"method",
	"class",
	"interface",
	"type",
	"constructor",
	"constant",
	"variable",
	"module",
];

// The backend has already ranked the complete result set. Keep that ordering
// stable and let the virtualizer limit DOM work instead of growing the scroll
// range page by page.
const ALL_RESULTS = -1;

type ContentRow =
	| { type: "hit"; hit: GraphContentHit }
	| { type: "raw-heading" }
	| { type: "raw"; hit: GraphRawContentHit }
	| { type: "raw-more"; count: number };

export type SearchMode = "symbols" | "content";

export function SearchView({
	seed,
	refreshKey,
	onExplore,
	onOpenFile,
}: {
	seed?: { query?: string; file?: string };
	refreshKey: number;
	onExplore: (node: GraphNode) => void;
	onOpenFile: (path: string, line?: number) => void;
}) {
	const [mode, setMode] = useState<SearchMode>("symbols");
	const [query, setQuery] = useState(seed?.query ?? "");
	const [kind, setKind] = useState("");
	const [file, setFile] = useState(seed?.file ?? "");
	const [regex, setRegex] = useState(false);
	const [caseSensitive, setCaseSensitive] = useState(false);
	const [sort, setSort] = useState("relevance");
	const [symbols, setSymbols] = useState<GraphSearchResult | null>(null);
	const [content, setContent] = useState<GraphContentSearchResult | null>(null);
	const [loading, setLoading] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const requestRef = useRef<AbortController | null>(null);
	const inputRef = useRef<HTMLInputElement | null>(null);
	const resultsRef = useRef<HTMLDivElement | null>(null);

	useEffect(() => {
		inputRef.current?.focus();
	}, []);

	useEffect(() => {
		requestRef.current?.abort();
		resultsRef.current?.scrollTo({ top: 0 });
		if (mode === "content" && !query.trim()) {
			setContent(null);
			setLoading(false);
			setError(null);
			return;
		}
		if (mode === "symbols") setSymbols(null);
		else setContent(null);
		const controller = new AbortController();
		requestRef.current = controller;
		const timer = window.setTimeout(() => {
			setLoading(true);
			setError(null);
			const request =
				mode === "symbols"
					? searchGraphSymbols(
							{ query: query.trim(), kind, file, sort, limit: ALL_RESULTS },
							controller.signal,
						).then(setSymbols)
					: searchGraphContent(
							{
								pattern: query,
								regex,
								ignore_case: !caseSensitive,
								file,
								sort,
								limit: ALL_RESULTS,
							},
							controller.signal,
						).then(setContent);
			request
				.then(() => setLoading(false))
				.catch((searchError: unknown) => {
					if (controller.signal.aborted) return;
					setError(
						searchError instanceof Error
							? searchError.message
							: "Search failed",
					);
					setLoading(false);
				});
		}, 250);
		return () => {
			window.clearTimeout(timer);
			controller.abort();
		};
	}, [mode, query, kind, file, regex, caseSensitive, sort, refreshKey]);

	const contentRows = useMemo<ContentRow[]>(() => {
		if (!content) return [];
		const rows: ContentRow[] = content.hits.map((hit) => ({ type: "hit", hit }));
		if ((content.raw?.length ?? 0) > 0) {
			rows.push({ type: "raw-heading" });
			for (const hit of content.raw ?? []) rows.push({ type: "raw", hit });
			if (content.raw_has_more) {
				rows.push({ type: "raw-more", count: content.raw?.length ?? 0 });
			}
		}
		return rows;
	}, [content]);
	const getSymbolKey = useCallback(
		(index: number) => symbols?.nodes[index]?.id ?? index,
		[symbols],
	);
	const getContentKey = useCallback(
		(index: number) => {
			const row = contentRows[index];
			if (!row) return index;
			if (row.type === "hit") return `hit:${row.hit.node.id}`;
			if (row.type === "raw") return `raw:${row.hit.file}:${row.hit.line}`;
			return row.type;
		},
		[contentRows],
	);
	const getContentSize = useCallback(
		(index: number) => (contentRows[index]?.type === "raw-heading" ? 34 : 27),
		[contentRows],
	);

	const symbolVirtualizer = useVirtualizer({
		count: mode === "symbols" ? (symbols?.nodes.length ?? 0) : 0,
		getScrollElement: () => resultsRef.current,
		getItemKey: getSymbolKey,
		estimateSize: () => 27,
		overscan: 12,
	});
	const contentVirtualizer = useVirtualizer({
		count: mode === "content" ? contentRows.length : 0,
		getScrollElement: () => resultsRef.current,
		getItemKey: getContentKey,
		estimateSize: getContentSize,
		overscan: 12,
	});
	const symbolVirtualItems = symbolVirtualizer.getVirtualItems();
	const contentVirtualItems = contentVirtualizer.getVirtualItems();

	const openNode = (node: GraphNode) =>
		onOpenFile(node.file, nodeTargetLine(node));

	return (
		<div className="flex h-full min-h-0 flex-col">
			<div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle bg-bg-surface/20 px-3 py-2">
				<div className="flex overflow-hidden rounded-md border border-border">
					{(["symbols", "content"] as const).map((value) => (
						<button
							key={value}
							type="button"
							onClick={() => {
								setMode(value);
								setSort("relevance");
								resultsRef.current?.scrollTo({ top: 0 });
							}}
							className={`px-2 py-1 text-[11px] capitalize transition-colors ${
								mode === value
									? "bg-bg-active text-fg"
									: "text-fg-dim hover:bg-bg-hover hover:text-fg-muted"
							}`}
						>
							{value}
						</button>
					))}
				</div>
				<div className="flex min-w-40 flex-1 items-center gap-1 rounded-md border border-border bg-bg-input px-2 py-1 focus-within:border-border-strong">
					<input
						ref={inputRef}
						value={query}
						onChange={(event) => setQuery(event.target.value)}
						placeholder={
							mode === "symbols"
								? "Search symbols by name…"
								: "Search code content…"
						}
						spellCheck={false}
						className="min-w-0 flex-1 bg-transparent text-[11px] text-fg outline-none placeholder:text-fg-dim"
					/>
					{loading && (
						<Loader2 size={11} className="shrink-0 animate-spin text-fg-dim" />
					)}
					{query && !loading && (
						<button
							type="button"
							title="Clear search"
							aria-label="Clear search"
							onClick={() => setQuery("")}
							className="grid h-4 w-4 shrink-0 place-items-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
						>
							<X size={10} />
						</button>
					)}
				</div>
				{mode === "symbols" ? (
					<select
						value={kind}
						onChange={(event) => setKind(event.target.value)}
						aria-label="Symbol kind"
						className="rounded-md border border-border bg-bg-input px-1.5 py-1 text-[11px] text-fg-muted outline-none"
					>
						<option value="">all kinds</option>
						{SYMBOL_KINDS.map((value) => (
							<option key={value} value={value}>
								{value}
							</option>
						))}
					</select>
				) : (
					<div className="flex items-center gap-0.5">
						<button
							type="button"
							title="Match case"
							aria-pressed={caseSensitive}
							onClick={() => setCaseSensitive((value) => !value)}
							className={`grid h-6 w-6 place-items-center rounded ${
								caseSensitive
									? "bg-bg-active text-fg"
									: "text-fg-dim hover:bg-bg-hover hover:text-fg"
							}`}
						>
							<CaseSensitive size={13} />
						</button>
						<button
							type="button"
							title="Regular expression"
							aria-pressed={regex}
							onClick={() => setRegex((value) => !value)}
							className={`grid h-6 w-6 place-items-center rounded ${
								regex
									? "bg-bg-active text-fg"
									: "text-fg-dim hover:bg-bg-hover hover:text-fg"
							}`}
						>
							<Regex size={13} />
						</button>
					</div>
				)}
				<select
					value={sort}
					onChange={(event) => {
						setSort(event.target.value);
						resultsRef.current?.scrollTo({ top: 0 });
					}}
					aria-label="Sort results"
					className="rounded-md border border-border bg-bg-input px-1.5 py-1 text-[11px] text-fg-muted outline-none"
				>
					<option value="relevance">relevance</option>
					{mode === "symbols" && <option value="name">name</option>}
					<option value="file">file</option>
					<option value="connections">connections</option>
					{mode === "content" && <option value="matches">matches</option>}
				</select>
				<input
					value={file}
					onChange={(event) => setFile(event.target.value)}
					placeholder="path filter"
					title="Only include paths containing this text"
					aria-label="Path filter"
					spellCheck={false}
					className="w-32 rounded-md border border-border bg-bg-input px-2 py-1 font-mono text-[11px] text-fg outline-none placeholder:text-fg-dim focus:border-border-strong"
				/>
			</div>
			{error && (
				<div className="shrink-0 border-b border-danger/20 bg-danger/5 px-3 py-1.5 text-[10px] text-danger/80">
					{error}
				</div>
			)}
			{mode === "symbols" && symbols && (
				<div className="shrink-0 border-b border-border-subtle px-3 py-1.5 text-[10px] text-fg-dim">
					{symbols.total.toLocaleString()} {symbols.total === 1 ? "symbol" : "symbols"}
				</div>
			)}
			{mode === "content" && content && (
				<div className="shrink-0 border-b border-border-subtle px-3 py-1.5 text-[10px] text-fg-dim">
					{content.total_line_hits.toLocaleString()} matching {content.total_line_hits === 1 ? "line" : "lines"} ·{" "}
					{content.total_results.toLocaleString()} {content.total_results === 1 ? "definition" : "definitions"}
					{content.total_raw_results > 0 && ` · ${content.total_raw_results.toLocaleString()} other locations`}
				</div>
			)}
			<div ref={resultsRef} className="min-h-0 flex-1 overflow-y-auto">
				{mode === "symbols" && symbols && symbols.nodes.length === 0 && (
					<div className="px-3 py-8 text-center text-[11px] text-fg-dim">
						No symbols match these filters.
					</div>
				)}
				{mode === "symbols" && symbols && symbols.nodes.length > 0 && (
					<div className="relative w-full" style={{ height: symbolVirtualizer.getTotalSize() }}>
						{symbolVirtualItems.map((item) => {
							const node = symbols.nodes[item.index];
							if (!node) return null;
							return (
								<div
									key={item.key}
									className="absolute top-0 left-0 w-full"
									style={{ height: item.size, transform: `translateY(${item.start}px)` }}
								>
									<NodeRow node={node} onOpen={openNode} onExplore={onExplore} />
								</div>
							);
						})}
					</div>
				)}
				{mode === "content" && !content && !error && (
					<div className="px-3 py-6 text-center text-[11px] text-fg-dim">
						Matches are grouped by the definition that contains them and ranked by relevance.
					</div>
				)}
				{mode === "content" && content && contentRows.length === 0 && (
					<div className="px-3 py-8 text-center text-[11px] text-fg-dim">
						No code matches these filters.
					</div>
				)}
				{mode === "content" && content && contentRows.length > 0 && (
					<div className="relative w-full" style={{ height: contentVirtualizer.getTotalSize() }}>
						{contentVirtualItems.map((item) => {
							const row = contentRows[item.index];
							if (!row) return null;
							return (
								<div
									key={item.key}
									className="absolute top-0 left-0 w-full"
									style={{ height: item.size, transform: `translateY(${item.start}px)` }}
								>
									{row.type === "hit" ? (
										<div className="group flex h-full w-full items-center gap-1.5 border-b border-border-subtle/60 px-2 text-[11px] hover:bg-bg-hover">
											<KindBadge kind={row.hit.node.kind} />
											<button
												type="button"
												title={`${row.hit.node.file}:${row.hit.match_lines[0] ?? row.hit.node.start_line}`}
												onClick={() => onOpenFile(row.hit.node.file, row.hit.match_lines[0] ?? row.hit.node.start_line)}
												className="flex min-w-0 flex-1 items-baseline gap-1.5 text-left"
											>
												<span className="truncate text-fg-muted group-hover:text-fg">{row.hit.node.name}</span>
												<span className="min-w-0 truncate font-mono text-[9px] text-fg-dim">{row.hit.node.file}</span>
											</button>
											<span className="shrink-0 text-[10px] text-fg-dim tabular-nums">
												{row.hit.match_lines.length} {row.hit.match_lines.length === 1 ? "line" : "lines"} · {row.hit.callers} in
											</span>
											<button
												type="button"
												title={`Explore ${row.hit.node.name}`}
												onClick={() => onExplore(row.hit.node)}
												className="hidden h-5 shrink-0 items-center rounded px-1.5 text-[10px] text-fg-dim hover:bg-bg-active hover:text-fg group-hover:flex"
											>
												relations
											</button>
										</div>
									) : row.type === "raw-heading" ? (
										<div className="flex h-full items-end px-3 pb-1.5 text-[10px] text-fg-dim">Other matching lines</div>
									) : row.type === "raw" ? (
										<button
											type="button"
											title={`${row.hit.file}:${row.hit.line}`}
											onClick={() => onOpenFile(row.hit.file, row.hit.line)}
											className="flex h-full w-full items-center gap-1.5 border-b border-border-subtle/60 px-2 text-left hover:bg-bg-hover"
										>
											<span className="shrink-0 font-mono text-[9px] text-fg-dim">{row.hit.file}:{row.hit.line}</span>
											<span className="min-w-0 flex-1 truncate font-mono text-[10px] text-fg-muted">{row.hit.content}</span>
										</button>
									) : (
										<div className="flex h-full items-center px-3 text-[10px] text-fg-dim">First {row.count} ungrouped matches shown</div>
									)}
								</div>
							);
						})}
					</div>
				)}
			</div>
		</div>
	);
}
