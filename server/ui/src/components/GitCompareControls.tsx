import {
	Check,
	ChevronDown,
	ChevronRight,
	GitCompareArrows,
	GitCommitHorizontal,
	History,
	Loader2,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { CompareMode, GitCommit, ServerMessage } from "../types/protocol";

type OpenCompare = (base: string, head: string, mode: CompareMode) => void;

export function GitHistoryPanel({
	disabled,
	subscribe,
	onCompare,
}: {
	disabled: boolean;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
	onCompare: OpenCompare;
}) {
	const [open, setOpen] = useState(false);
	const [loading, setLoading] = useState(false);
	const [loaded, setLoaded] = useState(false);
	const [error, setError] = useState("");
	const [commits, setCommits] = useState<GitCommit[]>([]);
	const [selection, setSelection] = useState<string[]>([]);

	const load = useCallback(async () => {
		setLoading(true);
		setError("");
		try {
			const response = await fetch("/api/git/history?limit=100");
			if (!response.ok) throw new Error(await responseError(response));
			setCommits((await response.json()) as GitCommit[]);
			setLoaded(true);
		} catch (e) {
			setError(e instanceof Error ? e.message : String(e));
		} finally {
			setLoading(false);
		}
	}, []);

	useEffect(() => {
		if (open && !loaded && !loading) void load();
	}, [open, loaded, loading, load]);
	useEffect(() => {
		if (!subscribe) return;
		return subscribe((message) => {
			if (message.type === "diffs_changed") {
				setLoaded(false);
				if (open) void load();
			}
		});
	}, [load, open, subscribe]);

	const select = (hash: string) => {
		setSelection((current) => {
			const index = current.indexOf(hash);
			if (index >= 0) return current.filter((value) => value !== hash);
			if (current.length < 2) return [...current, hash];
			return [hash];
		});
	};
	const selectedCommit =
		selection.length === 1
			? commits.find((commit) => commit.hash === selection[0])
			: undefined;
	const selectedParent = selectedCommit?.parents[0];
	const canCompare = selection.length === 2 || !!selectedParent;
	const compare = () => {
		if (selection.length === 2) {
			onCompare(selection[0], selection[1], "direct");
			return;
		}
		if (selection.length === 1 && selectedParent) {
			onCompare(selectedParent, selection[0], "direct");
		}
	};

	return (
		<div className="shrink-0 border-t border-border-subtle bg-bg-surface/15">
			<button
				type="button"
				onClick={() => setOpen((value) => !value)}
				aria-expanded={open}
				className="flex h-8 w-full items-center gap-1.5 px-3 text-[10.5px] text-fg-dim hover:bg-bg-hover hover:text-fg-muted"
			>
				{open ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
				<History size={11} />
				<span>History</span>
				{commits.length > 0 && (
					<span className="rounded-full bg-bg-active px-1.5 text-[9px] tabular-nums">
						{commits.length}
					</span>
				)}
				<div className="flex-1" />
				{selection.length > 0 && (
					<span className="text-[9.5px] text-fg-dim">
						{selection.length === 2
							? "Ready to compare"
							: selectedParent
								? "View this commit or select another"
								: "Select a second commit"}
					</span>
				)}
			</button>
			{open && (
				<div className="border-t border-border-subtle">
					{selection.length > 0 && (
						<div className="flex h-9 items-center gap-1.5 border-b border-border-subtle bg-bg-surface/20 px-2">
							<div className="min-w-0 flex-1 truncate font-mono text-[9.5px] text-fg-dim">
								{selection.length === 1 && selectedParent
									? `${selectedParent.slice(0, 7)} → ${selection[0].slice(0, 7)}`
									: `${selection[0]?.slice(0, 7)}${selection[1] ? ` → ${selection[1].slice(0, 7)}` : " → …"}`}
							</div>
							<button
								type="button"
								onClick={() => setSelection([])}
								className="h-6 rounded px-1.5 text-[9.5px] text-fg-dim hover:bg-bg-hover hover:text-fg"
							>
								Clear
							</button>
							<button
								type="button"
								disabled={disabled || !canCompare}
								onClick={compare}
								className="h-6 rounded bg-accent px-2 text-[9.5px] font-medium text-bg disabled:opacity-30"
							>
								{selection.length === 1 ? "View commit" : "Compare"}
							</button>
						</div>
					)}
					<div className="max-h-[300px] overflow-y-auto py-1">
						{loading && commits.length === 0 ? (
							<div className="flex h-16 items-center justify-center text-fg-dim">
								<Loader2 size={12} className="animate-spin" />
							</div>
						) : error ? (
							<div className="px-3 py-3 text-[10px] text-danger">{error}</div>
						) : commits.length === 0 ? (
							<div className="px-3 py-4 text-center text-[10px] text-fg-dim">
								No commits yet
							</div>
						) : (
							commits.map((commit, index) => (
								<CommitRow
									key={commit.hash}
									commit={commit}
									position={selection.indexOf(commit.hash)}
									singleSelected={selection.length === 1}
									last={index === commits.length - 1}
									disabled={disabled}
									onSelect={() => select(commit.hash)}
								/>
							))
						)}
					</div>
				</div>
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
	onSelect,
}: {
	commit: GitCommit;
	position: number;
	singleSelected: boolean;
	last: boolean;
	disabled: boolean;
	onSelect: () => void;
}) {
	const selectionLabel =
		position < 0
			? ""
			: singleSelected
				? "Commit: "
				: position === 0
					? "Base: "
					: "Compare: ";
	return (
		<button
			type="button"
			data-git-commit={commit.hash}
			disabled={disabled}
			onClick={onSelect}
			title={`${selectionLabel}${commit.summary}\n${commit.hash}`}
			className={`group flex min-h-9 w-full items-stretch pr-2 text-left transition-colors hover:bg-bg-hover ${position >= 0 ? "bg-accent/5" : ""} disabled:opacity-40`}
		>
			<span className="relative flex w-8 shrink-0 items-center justify-center">
				<span
					className={`absolute left-1/2 top-0 border-l border-border ${last ? "h-1/2" : "h-full"}`}
				/>
				<span
					className={`relative z-10 flex h-4 w-4 items-center justify-center rounded-full border bg-bg ${position >= 0 ? "border-accent text-accent" : "border-border text-fg-dim"}`}
				>
					{position >= 0 ? (
						<span className="text-[8px] font-bold">
							{singleSelected ? "C" : position === 0 ? "B" : "C"}
						</span>
					) : commit.parents.length > 1 ? (
						<GitCompareArrows size={8} />
					) : (
						<GitCommitHorizontal size={8} />
					)}
				</span>
			</span>
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
			<span className="flex w-4 shrink-0 items-center justify-center text-accent">
				{position >= 0 && <Check size={10} />}
			</span>
		</button>
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

async function responseError(response: Response) {
	return (
		(await response.text()).trim() ||
		`${response.status} ${response.statusText}`
	);
}
