import { useQuery } from "@tanstack/react-query";
import {
	ChevronDown,
	ChevronRight,
	ClipboardCopy,
	FileDiff,
	GitCompareArrows,
	GitCommitHorizontal,
	History,
	Loader2,
} from "lucide-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useEffect, useRef, useState } from "react";
import { getGitHistory } from "../api/git";
import { queryKeys } from "../api/query";
import type { CompareMode, GitCommit } from "../types/protocol";
import type { TabDisposition } from "../types/tabs";
import { FloatingMenu } from "./ui/Floating";

type OpenCompare = (
	base: string,
	head: string,
	mode: CompareMode,
	disposition?: TabDisposition,
) => void;

interface CommitMenu {
	x: number;
	y: number;
	commit: GitCommit;
}

const EMPTY_COMMITS: GitCommit[] = [];

export function GitHistoryPanel({
	open,
	disabled,
	onCompare,
	onToggle,
}: {
	open: boolean;
	disabled: boolean;
	onCompare: OpenCompare;
	onToggle: () => void;
}) {
	const historyQuery = useQuery({
		queryKey: queryKeys.git.history,
		enabled: open,
		staleTime: 0,
		queryFn: ({ signal }) => getGitHistory(signal),
	});
	const loading = historyQuery.isPending || historyQuery.isFetching;
	const error = historyQuery.error
		? historyQuery.error instanceof Error
			? historyQuery.error.message
			: String(historyQuery.error)
		: "";
	const commits = historyQuery.data ?? EMPTY_COMMITS;
	const [selection, setSelection] = useState<string[]>([]);
	const [menu, setMenu] = useState<CommitMenu | null>(null);
	const [alternateCopy, setAlternateCopy] = useState(false);
	const historyScrollRef = useRef<HTMLDivElement>(null);
	const historyVirtualizer = useVirtualizer({
		count: commits.length,
		getScrollElement: () => historyScrollRef.current,
		estimateSize: () => 36,
		getItemKey: (index) => commits[index]?.hash ?? index,
		overscan: 8,
	});

	useEffect(() => {
		const hashes = new Set(commits.map((commit) => commit.hash));
		setSelection((current) => current.filter((hash) => hashes.has(hash)));
	}, [commits]);
	useEffect(() => {
		if (!menu) return;
		const update = (event: KeyboardEvent) => setAlternateCopy(event.altKey);
		window.addEventListener("keydown", update);
		window.addEventListener("keyup", update);
		return () => {
			window.removeEventListener("keydown", update);
			window.removeEventListener("keyup", update);
		};
	}, [menu]);

	const openSelection = (revisions: string[], disposition: TabDisposition) => {
		if (revisions.length === 2) {
			onCompare(revisions[0], revisions[1], "direct", disposition);
			return;
		}
		const commit = commits.find((entry) => entry.hash === revisions[0]);
		if (!commit) return;
		onCompare(
			commit.parents[0] ?? ":empty",
			commit.hash,
			"direct",
			disposition,
		);
	};
	const toggleSelection = (hash: string) => {
		const next = selection.includes(hash)
			? selection.filter((value) => value !== hash)
			: selection.length === 0
				? [hash]
				: [selection[0], hash];
		setSelection(next);
		openSelection(next.length > 0 ? next : [hash], "preview");
	};
	const openCommit = (commit: GitCommit) => {
		openSelection([commit.hash], "keep");
		setMenu(null);
	};
	const copyCommit = async (commit: GitCommit) => {
		const hash = alternateCopy ? commit.hash.slice(0, 7) : commit.hash;
		await navigator.clipboard.writeText(hash);
		setMenu(null);
	};

	return (
		<div className="flex h-full min-h-0 flex-col border-t border-border-subtle bg-bg-surface/15">
			<button
				type="button"
				onClick={onToggle}
				aria-expanded={open}
				className="flex h-8 w-full shrink-0 items-center gap-1.5 px-3 text-[10.5px] text-fg-dim hover:bg-bg-hover hover:text-fg-muted"
			>
				{open ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
				<History size={11} />
				<span>History</span>
			</button>
			{open && (
				<div className="flex min-h-0 flex-1 flex-col border-t border-border-subtle">
					{commits.length === 0 ? (
						<div className="min-h-0 flex-1 overflow-y-auto py-1">
							{loading ? (
								<div className="flex h-16 items-center justify-center text-fg-dim">
									<Loader2 size={12} className="animate-spin" />
								</div>
							) : error ? (
								<div className="px-3 py-3 text-[10px] text-danger">{error}</div>
							) : commits.length === 0 ? (
								<div className="px-3 py-4 text-center text-[10px] text-fg-dim">
									No commits yet
								</div>
							) : null}
						</div>
					) : (
						<div
							ref={historyScrollRef}
							data-virtual-git-history
							aria-label="Git commit history"
							className="min-h-0 flex-1 overflow-y-auto py-1"
						>
							<div
								className="relative w-full"
								style={{ height: historyVirtualizer.getTotalSize() }}
							>
								{historyVirtualizer.getVirtualItems().map((virtualRow) => {
									const commit = commits[virtualRow.index];
									return (
										<div
											key={virtualRow.key}
											data-index={virtualRow.index}
											className="absolute left-0 top-0 w-full"
											style={{
												height: virtualRow.size,
												transform: `translateY(${virtualRow.start}px)`,
											}}
										>
											<CommitRow
												commit={commit}
												position={selection.indexOf(commit.hash)}
												singleSelected={selection.length === 1}
												last={virtualRow.index === commits.length - 1}
												disabled={disabled}
												onOpen={() => openSelection([commit.hash], "preview")}
												onSelect={() => toggleSelection(commit.hash)}
												onDoubleClick={() =>
													openSelection([commit.hash], "keep")
												}
												onContextMenu={(event) => {
													event.preventDefault();
													setAlternateCopy(event.altKey);
													setMenu({
														x: event.clientX,
														y: event.clientY,
														commit,
													});
												}}
											/>
										</div>
									);
								})}
							</div>
						</div>
					)}
				</div>
			)}
			{menu && (
				<FloatingMenu
					open
					onOpenChange={(nextOpen) => !nextOpen && setMenu(null)}
					reference={{ x: menu.x, y: menu.y }}
					label={`Actions for ${menu.commit.summary || menu.commit.hash.slice(0, 7)}`}
					className="z-[120] min-w-48 overflow-hidden rounded-md border border-border bg-bg-elevated py-1 shadow-xl"
				>
					<button
						type="button"
						role="menuitem"
						onClick={() => openCommit(menu.commit)}
						className="flex h-7 w-full items-center gap-2 px-2.5 text-left text-[10.5px] text-fg-muted hover:bg-bg-hover hover:text-fg disabled:opacity-35"
					>
						<FileDiff size={11} />
						Open commit changes
					</button>
					<div className="my-1 border-t border-border-subtle" />
					<button
						type="button"
						role="menuitem"
						onClick={() => void copyCommit(menu.commit)}
						className="flex h-7 w-full items-center gap-2 px-2.5 text-left text-[10.5px] text-fg-muted hover:bg-bg-hover hover:text-fg"
					>
						<ClipboardCopy size={11} />
						{alternateCopy ? "Copy short hash" : "Copy commit hash"}
						<span className="ml-auto text-[9px] text-fg-dim">Alt</span>
					</button>
				</FloatingMenu>
			)}
		</div>
	);
}

function CommitRow({
	commit,
	position,
	singleSelected,
	last,
	disabled,
	onOpen,
	onSelect,
	onDoubleClick,
	onContextMenu,
}: {
	commit: GitCommit;
	position: number;
	singleSelected: boolean;
	last: boolean;
	disabled: boolean;
	onOpen: () => void;
	onSelect: () => void;
	onDoubleClick: () => void;
	onContextMenu: (event: React.MouseEvent<HTMLDivElement>) => void;
}) {
	const commitLabel = commit.summary || commit.hash.slice(0, 7);
	const selected = position >= 0;
	const selectionRole = !selected
		? null
		: singleSelected
			? "commit"
			: position === 0
				? "base"
				: "comparison";
	const selectionAction = selectionRole
		? `Clear ${selectionRole} selection for ${commitLabel}`
		: `Select ${commitLabel} for comparison`;
	return (
		<div
			data-git-commit={commit.hash}
			onContextMenu={onContextMenu}
			className={`group flex h-9 w-full items-stretch text-left transition-colors hover:bg-bg-hover ${selected ? "bg-accent/5" : ""} ${disabled ? "opacity-40" : ""}`}
		>
			<button
				type="button"
				data-commit-select
				disabled={disabled}
				onClick={onSelect}
				aria-pressed={selected}
				aria-label={selectionAction}
				title={selectionAction}
				className="relative flex w-8 shrink-0 cursor-pointer items-center justify-center disabled:cursor-default"
			>
				<span
					className={`absolute left-1/2 top-0 border-l border-border ${last ? "h-1/2" : "h-full"}`}
				/>
				<span
					className={`relative z-10 flex h-4 w-4 items-center justify-center rounded-full border bg-bg transition-colors ${selected ? "border-accent text-accent" : "border-border text-fg-dim group-hover:border-fg-dim group-hover:text-fg-muted"}`}
				>
					{selected ? (
						<span className="text-[8px] font-bold">
							{singleSelected ? "C" : position === 0 ? "B" : "C"}
						</span>
					) : commit.parents.length > 1 ? (
						<GitCompareArrows size={8} />
					) : (
						<GitCommitHorizontal size={8} />
					)}
				</span>
			</button>
			<button
				type="button"
				disabled={disabled}
				onClick={onOpen}
				onDoubleClick={onDoubleClick}
				aria-label={`Open changes for ${commitLabel}`}
				title={`${commit.summary}\n${commit.hash}\nDouble-click to keep open`}
				className="flex min-w-0 flex-1 cursor-pointer items-stretch pr-2 text-left disabled:cursor-default"
			>
				<span className="min-w-0 flex-1 py-1">
					<span className="flex items-center gap-1">
						<span className="min-w-0 flex-1 truncate text-[10.5px] text-fg-muted group-hover:text-fg">
							{commit.summary || "Untitled commit"}
						</span>
						<span className="shrink-0 font-mono text-[9px] text-fg-dim">
							{commit.hash.slice(0, 7)}
						</span>
					</span>
					<span className="mt-0.5 flex min-w-0 items-center gap-1 text-[8.5px] text-fg-dim">
						{(commit.refs ?? []).map((ref) => (
							<span
								key={ref}
								className="max-w-24 truncate rounded bg-bg-active px-1 text-accent"
								title={ref}
							>
								{ref}
							</span>
						))}
						<span className="truncate">{commit.author}</span>
						<span className="shrink-0">
							· {formatCommitDate(commit.authored_at)}
						</span>
					</span>
				</span>
			</button>
		</div>
	);
}

function formatCommitDate(value: string) {
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	return new Intl.DateTimeFormat(undefined, {
		month: "short",
		day: "numeric",
	}).format(date);
}
