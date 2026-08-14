import { DiffEditor, type DiffOnMount } from "@monaco-editor/react";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
	ChevronDown,
	ChevronRight,
	FileDiff,
	GitCompareArrows,
	Loader2,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import { defineWingmanThemes, wingmanThemeName } from "../monacoThemes";
import type {
	CompareMode,
	DiffEntry,
	GitCompare,
	ServerMessage,
} from "../types/protocol";
import { DiffView } from "./DiffTab";

interface Props {
	base: string;
	head: string;
	mode: CompareMode;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function CompareTab({ base, head, mode, subscribe }: Props) {
	const [comparison, setComparison] = useState<GitCompare | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState("");
	const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
	const scheme = useColorScheme();

	const load = useCallback(async () => {
		setLoading(true);
		try {
			const params = new URLSearchParams({ base, head, mode });
			const response = await fetch(`/api/git/compare?${params.toString()}`);
			if (!response.ok) throw new Error(await responseError(response));
			const data = (await response.json()) as GitCompare;
			setComparison(data);
			setCollapsed(
				new Set(
					data.files
						.filter((file) => !isSummaryOnlyChange(file) && isLargeChange(file))
						.map((file) => file.path),
				),
			);
			setError("");
		} catch (e) {
			setComparison(null);
			setError(e instanceof Error ? e.message : String(e));
		} finally {
			setLoading(false);
		}
	}, [base, head, mode]);

	useEffect(() => {
		void load();
	}, [load]);
	useEffect(() => {
		if (!subscribe) return;
		return subscribe((message) => {
			if (message.type === "diffs_changed") void load();
		});
	}, [load, subscribe]);

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
	if (error) {
		return (
			<div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
				<div className="text-[12px] text-danger">{error}</div>
				<button
					type="button"
					onClick={() => void load()}
					className="rounded-md border border-border-subtle px-3 py-1.5 text-[11px] text-fg-muted hover:bg-bg-hover hover:text-fg"
				>
					Try again
				</button>
			</div>
		);
	}
	if (!comparison) return null;

	return (
		<div className="flex h-full flex-col overflow-hidden bg-bg">
			<div className="flex min-h-12 shrink-0 items-center gap-3 border-b border-border-subtle bg-bg-surface/30 px-4">
				<GitCompareArrows size={15} className="shrink-0 text-accent" />
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
					<span className="text-success">+{totals.additions}</span>
					<span className="text-danger">−{totals.deletions}</span>
					{largeFiles > 0 && (
						<span title="Generated, minified, or unusually large diffs start collapsed">
							{largeFiles} large {largeFiles === 1 ? "diff" : "diffs"}
						</span>
					)}
				</div>
			</div>
			{mode === "merge-base" && comparison.merge_base_hash && (
				<div className="shrink-0 border-b border-border-subtle bg-bg-surface/15 px-4 py-1.5 text-[10px] text-fg-dim">
					Pull-request comparison from merge base{" "}
					<span className="font-mono">
						{comparison.merge_base_hash.slice(0, 7)}
					</span>
				</div>
			)}

			<div className="min-h-0 flex-1 overflow-hidden">
				{comparison.files.length === 0 ? (
					<div className="flex min-h-32 items-center justify-center text-[12px] text-fg-dim">
						No file changes between these revisions
					</div>
				) : (
					<VirtualCompareFiles
						files={comparison.files}
						collapsed={collapsed}
						scheme={scheme}
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
	scheme,
	onToggle,
}: {
	files: DiffEntry[];
	collapsed: Set<string>;
	scheme: "light" | "dark";
	onToggle: (path: string) => void;
}) {
	const scrollRef = useRef<HTMLDivElement>(null);
	const virtualizer = useVirtualizer({
		count: files.length,
		getScrollElement: () => scrollRef.current,
		getItemKey: (index) =>
			`${files[index].original_path ?? ""}:${files[index].path}`,
		estimateSize: (index) =>
			compareRowHeight(files[index], collapsed.has(files[index].path)),
		overscan: 1,
		paddingStart: 12,
		paddingEnd: 12,
	});

	useEffect(() => {
		virtualizer.measure();
	}, [collapsed, files, virtualizer]);
	const virtualRows = virtualizer.getVirtualItems();
	const scrollOffset = virtualizer.scrollOffset ?? 0;
	const activeRow =
		virtualRows.find((row) => row.end > scrollOffset + 12) ?? virtualRows[0];
	const activeFile = activeRow ? files[activeRow.index] : undefined;
	const showStickyHeader = !!activeRow && scrollOffset > activeRow.start + 36;

	return (
		<div
			ref={scrollRef}
			className="h-full overflow-y-auto"
			data-virtual-compare-list
		>
			{activeFile && showStickyHeader && (
				<div className="pointer-events-none sticky top-0 z-30 h-0 px-3">
					<div
						className="pointer-events-auto mx-auto w-full max-w-[1576px] overflow-hidden rounded-b-md border-x border-b border-border bg-bg-elevated shadow-lg"
						data-sticky-file-header={activeFile.path}
					>
						<FileHeader
							file={activeFile}
							closed={collapsed.has(activeFile.path)}
							onToggle={onToggle}
						/>
					</div>
				</div>
			)}
			<div
				className="relative mx-auto w-full max-w-[1600px]"
				style={{ height: virtualizer.getTotalSize() }}
			>
				{virtualRows.map((virtualRow) => {
					const file = files[virtualRow.index];
					const summaryOnly = isSummaryOnlyChange(file);
					const closed = collapsed.has(file.path);
					return (
						<div
							key={virtualRow.key}
							data-index={virtualRow.index}
							ref={virtualizer.measureElement}
							className="absolute left-0 top-0 w-full px-3 pb-3"
							style={{ transform: `translateY(${virtualRow.start}px)` }}
						>
							<section
								data-compare-file={file.path}
								data-summary-only={summaryOnly || undefined}
								data-large-diff={
									(!summaryOnly && isLargeChange(file)) || undefined
								}
								className="overflow-hidden rounded-md border border-border-subtle bg-bg-surface/10"
							>
								<FileHeader file={file} closed={closed} onToggle={onToggle} />
								{!summaryOnly && !closed && (
									<CompareFile file={file} scheme={scheme} />
								)}
							</section>
						</div>
					);
				})}
			</div>
		</div>
	);
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
	return (
		<button
			type="button"
			disabled={summaryOnly}
			onClick={() => !summaryOnly && onToggle(file.path)}
			aria-expanded={summaryOnly ? undefined : !closed}
			className="flex h-9 w-full items-center gap-2 border-b border-border-subtle bg-bg-surface/70 px-3 text-left disabled:cursor-default"
		>
			{summaryOnly ? (
				<span className="w-3 shrink-0" />
			) : closed ? (
				<ChevronRight size={12} className="shrink-0 text-fg-dim" />
			) : (
				<ChevronDown size={12} className="shrink-0 text-fg-dim" />
			)}
			<FileDiff size={12} className="shrink-0 text-fg-dim" />
			<FilePath file={file} />
			<FileStatus file={file} />
			{!summaryOnly && isLargeChange(file) && (
				<span className="shrink-0 rounded bg-bg-active px-1.5 py-0.5 text-[9px] text-fg-dim">
					Large diff
				</span>
			)}
		</button>
	);
}

function FilePath({ file }: { file: DiffEntry }) {
	if (file.original_path && file.original_path !== file.path) {
		return (
			<span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg-muted">
				{file.original_path} <span className="text-fg-dim">→</span> {file.path}
			</span>
		);
	}
	const name = file.path.split("/").pop() || file.path;
	const directory = file.path.slice(0, -name.length);
	return (
		<span className="flex min-w-0 flex-1 items-center font-mono text-[11.5px]">
			{directory && <span className="truncate text-fg-dim">{directory}</span>}
			<span className="shrink-0 text-fg-muted">{name}</span>
		</span>
	);
}

function CompareFile({
	file,
	scheme,
}: {
	file: DiffEntry;
	scheme: "light" | "dark";
}) {
	const [editorHeight, setEditorHeight] = useState(96);
	const editorCleanup = useRef<Array<{ dispose: () => void }>>([]);
	const resizeFrame = useRef<number | null>(null);
	useEffect(
		() => () => {
			for (const disposable of editorCleanup.current) disposable.dispose();
			if (resizeFrame.current !== null)
				cancelAnimationFrame(resizeFrame.current);
		},
		[],
	);
	const mountEditor: DiffOnMount = (editor) => {
		for (const disposable of editorCleanup.current) disposable.dispose();
		const resize = () => {
			resizeFrame.current = null;
			const height = Math.max(
				96,
				Math.ceil(
					Math.max(
						editor.getOriginalEditor().getContentHeight(),
						editor.getModifiedEditor().getContentHeight(),
					),
				),
			);
			setEditorHeight(height);
		};
		const scheduleResize = () => {
			if (resizeFrame.current !== null)
				cancelAnimationFrame(resizeFrame.current);
			resizeFrame.current = requestAnimationFrame(resize);
		};
		editorCleanup.current = [editor.onDidUpdateDiff(scheduleResize)];
		scheduleResize();
	};
	const hasText = file.original !== undefined || file.modified !== undefined;
	if (!hasText) {
		return (
			<div className="bg-bg">
				<DiffView patch={file.patch} />
			</div>
		);
	}
	return (
		<DiffEditor
			className="wingman-static-diff"
			height={editorHeight}
			language={isLargeChange(file) ? "plaintext" : file.language || undefined}
			original={file.original ?? ""}
			modified={file.modified ?? ""}
			theme={wingmanThemeName(scheme)}
			beforeMount={defineWingmanThemes}
			onMount={mountEditor}
			options={{
				readOnly: true,
				renderSideBySide: true,
				renderOverviewRuler: false,
				overviewRulerLanes: 0,
				overviewRulerBorder: false,
				minimap: { enabled: false },
				fontSize: 12,
				lineNumbers: "on",
				scrollBeyondLastLine: false,
				renderWhitespace: "none",
				padding: { top: 8 },
				hideUnchangedRegions: { enabled: true },
				scrollbar: {
					vertical: "hidden",
					horizontal: "auto",
					handleMouseWheel: false,
					alwaysConsumeMouseWheel: false,
				},
			}}
		/>
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

function diffEditorHeight(file: DiffEntry) {
	return Math.max(180, file.patch.split("\n").length * 19 + 48);
}

function compareRowHeight(file: DiffEntry, collapsed: boolean) {
	if (collapsed || isSummaryOnlyChange(file)) return 48;
	const contentHeight =
		file.original !== undefined || file.modified !== undefined
			? diffEditorHeight(file)
			: file.patch.split("\n").length * 19 + 16;
	return 48 + contentHeight;
}

function isLargeChange(file: DiffEntry) {
	const path = file.path.toLowerCase();
	const generated =
		/(^|\/)(?:build|dist|generated|node_modules|vendor)\//.test(path) ||
		/(?:\.min\.(?:css|js|mjs)|(?:^|[._-])generated[._-]|(?:package-lock\.json|pnpm-lock\.yaml|yarn\.lock))$/.test(
			path,
		);
	const patchLines = file.patch.split("\n").length;
	const contentSize =
		(file.original?.length ?? 0) + (file.modified?.length ?? 0);
	return generated || patchLines > 750 || contentSize > 250_000;
}

function isSummaryOnlyChange(file: DiffEntry) {
	return (
		file.status === "deleted" ||
		(!!file.original_path && file.original_path !== file.path) ||
		!hasContentChanges(file)
	);
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
		for (const line of file.patch.split("\n")) {
			if (line.startsWith("+") && !line.startsWith("+++")) additions++;
			if (line.startsWith("-") && !line.startsWith("---")) deletions++;
		}
	}
	return { additions, deletions };
}

async function responseError(response: Response) {
	return (
		(await response.text()).trim() ||
		`${response.status} ${response.statusText}`
	);
}
