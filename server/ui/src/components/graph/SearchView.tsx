import { CaseSensitive, Loader2, Regex } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import {
	type GraphContentSearchResult,
	type GraphNode,
	type GraphSearchResult,
	searchGraphContent,
	searchGraphSymbols,
} from "../../api/graph";
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

export type SearchMode = "symbols" | "content";

export function SearchView({
	seed,
	onExplore,
	onOpenFile,
}: {
	seed?: { query?: string; file?: string };
	onExplore: (node: GraphNode) => void;
	onOpenFile: (path: string, line?: number) => void;
}) {
	const [mode, setMode] = useState<SearchMode>("symbols");
	const [query, setQuery] = useState(seed?.query ?? "");
	const [kind, setKind] = useState("");
	const [file, setFile] = useState(seed?.file ?? "");
	const [regex, setRegex] = useState(false);
	const [caseSensitive, setCaseSensitive] = useState(false);
	const [symbols, setSymbols] = useState<GraphSearchResult | null>(null);
	const [content, setContent] = useState<GraphContentSearchResult | null>(null);
	const [loading, setLoading] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const requestRef = useRef<AbortController | null>(null);
	const inputRef = useRef<HTMLInputElement | null>(null);

	useEffect(() => {
		inputRef.current?.focus();
	}, []);

	useEffect(() => {
		requestRef.current?.abort();
		if (mode === "content" && !query.trim()) {
			setContent(null);
			setLoading(false);
			setError(null);
			return;
		}
		const controller = new AbortController();
		requestRef.current = controller;
		const timer = window.setTimeout(() => {
			setLoading(true);
			setError(null);
			const request =
				mode === "symbols"
					? searchGraphSymbols(
							{ query: query.trim(), kind, file, limit: 100 },
							controller.signal,
						).then(setSymbols)
					: searchGraphContent(
							{
								pattern: query,
								regex,
								ignore_case: !caseSensitive,
								file,
								limit: 50,
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
	}, [mode, query, kind, file, regex, caseSensitive]);

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
							onClick={() => setMode(value)}
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
				<input
					value={file}
					onChange={(event) => setFile(event.target.value)}
					placeholder="path filter"
					spellCheck={false}
					className="w-32 rounded-md border border-border bg-bg-input px-2 py-1 font-mono text-[11px] text-fg outline-none placeholder:text-fg-dim focus:border-border-strong"
				/>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto">
				{error && (
					<div className="mx-3 mt-2 rounded bg-danger/5 px-2 py-1.5 text-[10px] text-danger/80">
						{error}
					</div>
				)}
				{mode === "symbols" && symbols && (
					<>
						<div className="px-3 py-1.5 text-[10px] text-fg-dim">
							{symbols.total.toLocaleString()}{" "}
							{symbols.total === 1 ? "symbol" : "symbols"}
							{symbols.has_more
								? ` · showing first ${symbols.nodes.length}`
								: ""}
						</div>
						{symbols.nodes.map((node) => (
							<NodeRow
								key={node.id}
								node={node}
								detail={`:${node.start_line}`}
								onOpen={openNode}
								onExplore={onExplore}
							/>
						))}
					</>
				)}
				{mode === "content" && content && (
					<>
						<div className="px-3 py-1.5 text-[10px] text-fg-dim">
							{content.total_line_hits.toLocaleString()}{" "}
							{content.total_line_hits === 1 ? "match" : "matches"} in{" "}
							{content.total_results.toLocaleString()}{" "}
							{content.total_results === 1 ? "definition" : "definitions"}
						</div>
						{content.hits.map((hit) => (
							<div
								key={hit.node.id}
								className="group flex w-full items-center gap-1.5 border-b border-border-subtle/60 px-2 py-1 text-[11px] last:border-b-0 hover:bg-bg-hover"
							>
								<KindBadge kind={hit.node.kind} />
								<button
									type="button"
									title={`${hit.node.file}:${hit.match_lines[0] ?? hit.node.start_line}`}
									onClick={() =>
										onOpenFile(
											hit.node.file,
											hit.match_lines[0] ?? hit.node.start_line,
										)
									}
									className="flex min-w-0 flex-1 cursor-pointer items-baseline gap-1.5 text-left"
								>
									<span className="truncate text-fg-muted group-hover:text-fg">
										{hit.node.name}
									</span>
									<span className="min-w-0 truncate font-mono text-[9px] text-fg-dim">
										{hit.node.file}
									</span>
								</button>
								<span className="shrink-0 text-[10px] text-fg-dim tabular-nums">
									{hit.match_lines.length}{" "}
									{hit.match_lines.length === 1 ? "line" : "lines"} ·{" "}
									{hit.callers} in
								</span>
								<button
									type="button"
									title={`Explore ${hit.node.name}`}
									onClick={() => onExplore(hit.node)}
									className="hidden h-5 shrink-0 items-center rounded px-1.5 text-[10px] text-fg-dim hover:bg-bg-active hover:text-fg group-hover:flex"
								>
									graph
								</button>
							</div>
						))}
						{(content.raw?.length ?? 0) > 0 && (
							<>
								<div className="px-3 pt-3 pb-1.5 text-[10px] uppercase tracking-wider text-fg-dim">
									Outside indexed definitions
								</div>
								{content.raw?.map((hit) => (
									<button
										key={`${hit.file}:${hit.line}`}
										type="button"
										title={`${hit.file}:${hit.line}`}
										onClick={() => onOpenFile(hit.file, hit.line)}
										className="flex w-full items-baseline gap-1.5 border-b border-border-subtle/60 px-2 py-1 text-left text-[11px] last:border-b-0 hover:bg-bg-hover"
									>
										<span className="shrink-0 font-mono text-[9px] text-fg-dim">
											{hit.file}:{hit.line}
										</span>
										<span className="min-w-0 flex-1 truncate font-mono text-[10px] text-fg-muted">
											{hit.content}
										</span>
									</button>
								))}
							</>
						)}
					</>
				)}
				{mode === "content" && !content && !error && (
					<div className="px-3 py-6 text-center text-[11px] text-fg-dim">
						Matches are grouped by the function or type that contains them and
						ranked by how connected that code is.
					</div>
				)}
			</div>
		</div>
	);
}
