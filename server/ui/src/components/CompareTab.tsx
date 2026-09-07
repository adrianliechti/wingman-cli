import { defaultRangeExtractor, useVirtualizer } from "@tanstack/react-virtual";
import type { Range } from "@tanstack/react-virtual";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Loader2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fetchGitComparison } from "../api/git";
import { queryKeys } from "../api/query";
import type { CompareMode, DiffEntry, GitCompare } from "../types/protocol";
import { DiffView } from "./DiffTab";

interface Props {
	base: string;
	head: string;
	mode: CompareMode;
}

export function CompareTab({ base, head, mode }: Props) {
	const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
	const [collapsedFor, setCollapsedFor] = useState<GitCompare | null>(null);
	const comparisonQuery = useQuery({
		queryKey: queryKeys.git.compare(base, head, mode),
		// Branch names and :worktree can resolve differently each time a tab opens.
		staleTime: 0,
		queryFn: ({ signal }) => fetchGitComparison(base, head, mode, signal),
	});
	const comparison = comparisonQuery.data ?? null;
	const loading = comparisonQuery.isPending;
	const failure = comparisonQuery.error;

	if (comparison && comparison !== collapsedFor) {
		setCollapsedFor(comparison);
		setCollapsed(
			new Set(
				comparison.files
					.filter((file) => !isSummaryOnlyChange(file) && isLargeChange(file))
					.map((file) => file.path),
			),
		);
	}

	const totals = useMemo(
		() => countChanges(comparison?.files ?? []),
		[comparison],
	);
	const largeFiles = useMemo(
		() =>
			comparison?.files.filter(
				(file) => !isSummaryOnlyChange(file) && isLargeChange(file),
			).length ?? 0,
		[comparison],
	);

	if (loading && !comparison) {
		return (
			<div className="flex h-full items-center justify-center gap-2 text-[12px] text-fg-dim">
				<Loader2 size={13} className="animate-spin" />
				Comparing revisions…
			</div>
		);
	}
	if (failure) throw failure;
	if (!comparison) return null;

	return (
		<div className="flex h-full flex-col overflow-hidden bg-bg">
			<div className="flex min-h-10 w-full shrink-0 items-center gap-3 border-b border-border-subtle pr-5 pl-2">
				<div className="flex min-w-0 flex-1 items-center gap-2 font-mono text-[11px]">
					<RevisionLabel label={base} hash={comparison.base_hash} />
					<span className="text-fg-dim">→</span>
					<RevisionLabel label={head} hash={comparison.head_hash} />
				</div>
				<div className="flex shrink-0 items-center gap-2 text-[10.5px] text-fg-dim">
					<span>
						{comparison.files.length} changed{" "}
						{comparison.files.length === 1 ? "file" : "files"}
					</span>
					{largeFiles > 0 && (
						<span title="Generated, minified, or unusually large diffs start collapsed">
							{largeFiles} large {largeFiles === 1 ? "diff" : "diffs"}
						</span>
					)}
					<ChangeCounts changes={totals} />
				</div>
			</div>
			<div className="min-h-0 flex-1 overflow-hidden">
				{comparison.files.length === 0 ? (
					<div className="flex min-h-32 items-center justify-center text-[12px] text-fg-dim">
						No file changes between these revisions
					</div>
				) : (
					<VirtualCompareFiles
						files={comparison.files}
						collapsed={collapsed}
						onToggle={(path) =>
							setCollapsed((current) => togglePath(current, path))
						}
					/>
				)}
			</div>
		</div>
	);
}

function VirtualCompareFiles({
	files,
	collapsed,
	onToggle,
}: {
	files: DiffEntry[];
	collapsed: Set<string>;
	onToggle: (path: string) => void;
}) {
	"use no memo"; // TanStack Virtual's imperative instance must not be compiler-memoized.

	const scrollRef = useRef<HTMLDivElement>(null);
	const items = useMemo<CompareVirtualItem[]>(() => {
		const result: CompareVirtualItem[] = [];
		files.forEach((file, fileIndex) => {
			const key = `${file.original_path ?? ""}:${file.path}`;
			result.push({ kind: "header", fileIndex, key: `${key}:header` });
			if (!isSummaryOnlyChange(file) && !collapsed.has(file.path)) {
				result.push({ kind: "body", fileIndex, key: `${key}:body` });
			}
		});
		return result;
	}, [collapsed, files]);
	const stickyIndexes = useMemo(
		() =>
			items.flatMap((item, index) => (item.kind === "header" ? [index] : [])),
		[items],
	);
	const activeStickyIndexRef = useRef(0);
	const rangeExtractor = useCallback(
		(range: Range) => {
			for (let index = stickyIndexes.length - 1; index >= 0; index--) {
				if (range.startIndex >= stickyIndexes[index]) {
					activeStickyIndexRef.current = stickyIndexes[index];
					break;
				}
			}
			return Array.from(
				new Set([
					activeStickyIndexRef.current,
					...defaultRangeExtractor(range),
				]),
			).sort((a, b) => a - b);
		},
		[stickyIndexes],
	);
	// oxlint-disable-next-line react/incompatible-library -- Compiler memoization is explicitly disabled for this component.
	const virtualizer = useVirtualizer({
		count: items.length,
		getScrollElement: () => scrollRef.current,
		getItemKey: (index) => items[index].key,
		estimateSize: (index) =>
			items[index].kind === "header"
				? 36
				: compareBodyHeight(files[items[index].fileIndex]),
		overscan: 1,
		rangeExtractor,
	});

	useEffect(() => {
		virtualizer.measure();
	}, [items, virtualizer]);
	const virtualRows = virtualizer.getVirtualItems();

	return (
		<div
			ref={scrollRef}
			className="h-full overflow-y-auto"
			data-virtual-compare-list
		>
			<div
				className="relative w-full"
				style={{ height: virtualizer.getTotalSize() }}
			>
				{virtualRows.map((virtualRow) => {
					const item = items[virtualRow.index];
					const file = files[item.fileIndex];
					const summaryOnly = isSummaryOnlyChange(file);
					const closed = collapsed.has(file.path);
					const activeSticky =
						item.kind === "header" &&
						virtualRow.index === activeStickyIndexRef.current;
					const pinned =
						activeSticky && (virtualizer.scrollOffset ?? 0) > virtualRow.start;

					if (item.kind === "header") {
						return (
							<div
								key={virtualRow.key}
								data-index={virtualRow.index}
								ref={virtualizer.measureElement}
								className={`${activeSticky ? "sticky top-0 z-20" : "absolute left-0 top-0"} w-full px-2`}
								style={
									activeSticky
										? undefined
										: { transform: `translateY(${virtualRow.start}px)` }
								}
							>
								<section
									data-compare-file={file.path}
									data-active-file-header={activeSticky || undefined}
									data-summary-only={summaryOnly || undefined}
									data-large-diff={
										(!summaryOnly && isLargeChange(file)) || undefined
									}
									className={`bg-bg-surface ${pinned ? "shadow-[0_1px_0_var(--color-border),0_3px_8px_rgba(0,0,0,0.15)]" : ""}`}
								>
									<FileHeader file={file} closed={closed} onToggle={onToggle} />
								</section>
							</div>
						);
					}

					return (
						<div
							key={virtualRow.key}
							data-index={virtualRow.index}
							ref={virtualizer.measureElement}
							className="absolute left-0 top-0 w-full px-2"
							style={{ transform: `translateY(${virtualRow.start}px)` }}
						>
							<div className="overflow-x-auto bg-bg">
								<DiffView patch={file.patch} />
							</div>
						</div>
					);
				})}
			</div>
		</div>
	);
}

interface CompareVirtualItem {
	kind: "header" | "body";
	fileIndex: number;
	key: string;
}

function FileHeader({
	file,
	closed,
	onToggle,
}: {
	file: DiffEntry;
	closed: boolean;
	onToggle: (path: string) => void;
}) {
	const summaryOnly = isSummaryOnlyChange(file);
	const changes = useMemo(() => countFileChanges(file), [file]);
	return (
		<button
			type="button"
			disabled={summaryOnly}
			onClick={() => !summaryOnly && onToggle(file.path)}
			aria-expanded={summaryOnly ? undefined : !closed}
			className="flex h-9 w-full items-center gap-2 bg-bg-surface px-3 text-left transition-colors enabled:hover:bg-bg-hover disabled:cursor-default"
		>
			{summaryOnly ? (
				<span className="w-3 shrink-0" />
			) : closed ? (
				<ChevronRight size={12} className="shrink-0 text-fg-dim" />
			) : (
				<ChevronDown size={12} className="shrink-0 text-fg-dim" />
			)}
			<span className="flex min-w-0 flex-1 items-center gap-2">
				<FilePath file={file} />
				<FileStatus file={file} />
				{!summaryOnly && isLargeChange(file) && (
					<span className="shrink-0 text-[9px] text-fg-dim">Large diff</span>
				)}
			</span>
			<ChangeCounts changes={changes} />
		</button>
	);
}

function FilePath({ file }: { file: DiffEntry }) {
	if (file.original_path && file.original_path !== file.path) {
		return (
			<span className="min-w-0 truncate font-mono text-[11.5px] text-fg-muted">
				{file.original_path} <span className="text-fg-dim">→</span> {file.path}
			</span>
		);
	}
	const name = file.path.split("/").pop() || file.path;
	const directory = file.path.slice(0, -name.length);
	return (
		<span className="flex min-w-0 items-center font-mono text-[11.5px]">
			{directory && <span className="truncate text-fg-dim">{directory}</span>}
			<span className="shrink-0 text-fg-muted">{name}</span>
		</span>
	);
}

function ChangeCounts({
	changes,
}: {
	changes: { additions: number; deletions: number };
}) {
	if (changes.additions === 0 && changes.deletions === 0) {
		return <span className="w-20 shrink-0" />;
	}
	return (
		<span className="grid w-20 shrink-0 grid-cols-2 gap-2 text-right text-[10.5px] tabular-nums">
			<span className="text-success">+{changes.additions}</span>
			<span className="text-danger">−{changes.deletions}</span>
		</span>
	);
}

function RevisionLabel({ label, hash }: { label: string; hash: string }) {
	const displayLabel =
		label === ":worktree"
			? "Working tree"
			: label === ":empty"
				? "Empty tree"
				: label;
	return (
		<span
			className="flex min-w-0 items-center gap-1.5"
			title={hash ? `${displayLabel} (${hash})` : displayLabel}
		>
			<span className="max-w-52 truncate text-fg-muted">{displayLabel}</span>
			{hash && <span className="text-fg-dim">{hash.slice(0, 7)}</span>}
		</span>
	);
}

function FileStatus({ file }: { file: DiffEntry }) {
	const renamed = file.original_path && file.original_path !== file.path;
	const label = renamed
		? "Renamed"
		: file.status === "added"
			? "Added"
			: file.status === "deleted"
				? "Deleted"
				: hasContentChanges(file)
					? "Modified"
					: "Metadata";
	const color =
		file.status === "added"
			? "text-success"
			: file.status === "deleted"
				? "text-danger"
				: "text-warning";
	return <span className={`shrink-0 text-[10px] ${color}`}>{label}</span>;
}

function togglePath(current: Set<string>, path: string) {
	const next = new Set(current);
	if (next.has(path)) next.delete(path);
	else next.add(path);
	return next;
}

function compareBodyHeight(file: DiffEntry) {
	return 16 + file.patch.split("\n").length * 19;
}

function isLargeChange(file: DiffEntry) {
	const path = file.path.toLowerCase();
	const generated =
		/(^|\/)(?:build|dist|generated|node_modules|vendor)\//.test(path) ||
		/(?:\.min\.(?:css|js|mjs)|(?:^|[._-])generated[._-]|(?:package-lock\.json|pnpm-lock\.yaml|yarn\.lock))$/.test(
			path,
		);
	const patchLines = file.patch.split("\n").length;
	return generated || patchLines > 750 || file.patch.length > 250_000;
}

function isSummaryOnlyChange(file: DiffEntry) {
	return file.status === "deleted" || !hasContentChanges(file);
}

function hasContentChanges(file: DiffEntry) {
	return file.patch
		.split("\n")
		.some(
			(line) =>
				(line.startsWith("+") && !line.startsWith("+++")) ||
				(line.startsWith("-") && !line.startsWith("---")),
		);
}

function countChanges(files: DiffEntry[]) {
	let additions = 0;
	let deletions = 0;
	for (const file of files) {
		const changes = countFileChanges(file);
		additions += changes.additions;
		deletions += changes.deletions;
	}
	return { additions, deletions };
}

function countFileChanges(file: DiffEntry) {
	let additions = 0;
	let deletions = 0;
	for (const line of file.patch.split("\n")) {
		if (line.startsWith("+") && !line.startsWith("+++")) additions++;
		if (line.startsWith("-") && !line.startsWith("---")) deletions++;
	}
	return { additions, deletions };
}
