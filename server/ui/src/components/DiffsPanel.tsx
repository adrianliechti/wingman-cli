import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	ArrowDownToLine,
	ArrowUpFromLine,
	Check,
	CheckCircle2,
	ChevronDown,
	ClipboardCopy,
	Code2,
	FileText,
	GitBranch,
	GitCompareArrows,
	GitCommitHorizontal,
	Loader2,
	Minus,
	Plus,
	RefreshCw,
	RotateCcw,
	Search,
	Sparkles,
} from "lucide-react";
import {
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
} from "react";
import {
	Group,
	Panel,
	type PanelSize,
	Separator,
	usePanelRef,
} from "react-resizable-panels";
import { listDiffs, revertDiff } from "../api/diffs";
import {
	fetchGitBranches,
	getGitBranches,
	getGitStatus,
	generateGitCommitMessage,
	initializeGitRepository,
	runGitCommand,
	type GitCommand,
	type GitCommandType,
} from "../api/git";
import { invalidateGitIndexQueries, queryKeys } from "../api/query";
import type {
	DiffEntry,
	DiffLayer,
	CompareMode,
	GitBranch as GitBranchInfo,
	GitFileStatus,
	GitStatus,
} from "../types/protocol";
import type { TabDisposition } from "../types/tabs";
import { PanelEmptyState } from "./ui/EmptyState";
import { Dialog, dialogButtonClass } from "./ui/Feedback";
import { FloatingMenu, FloatingSurface } from "./ui/Floating";
import { GitHistoryPanel } from "./GitCompareControls";

interface Props {
	git?: boolean;
	canInit?: boolean;
	onOpenDiff?: (
		path: string,
		layer?: DiffLayer,
		disposition?: TabDisposition,
	) => void;
	onOpenCompare?: (
		base: string,
		head: string,
		mode: CompareMode,
		disposition?: TabDisposition,
	) => void;
	onOpenFile?: (path: string, disposition?: TabDisposition) => void;
}

interface MenuState {
	x: number;
	y: number;
	diff: DiffEntry;
}

interface GitMenuState {
	x: number;
	y: number;
	file: GitFileStatus;
	staged: boolean;
}

const EMPTY_GIT_STATUS: GitStatus = {
	branch: "",
	ahead: 0,
	behind: 0,
	has_remote: false,
	files: [],
};
const EMPTY_BRANCHES: GitBranchInfo[] = [];

export function DiffsPanel({
	git = false,
	canInit = false,
	onOpenDiff,
	onOpenCompare,
	onOpenFile,
}: Props) {
	const queryClient = useQueryClient();
	const changesQuery = useQuery<GitStatus | DiffEntry[]>({
		queryKey: git ? queryKeys.git.status : queryKeys.diffs.list(),
		enabled: git || !canInit,
		queryFn: ({ signal }) =>
			git ? getGitStatus(signal) : listDiffs({ signal }),
	});
	const diffs = git
		? []
		: ((changesQuery.data as DiffEntry[] | undefined) ?? []);
	const gitStatus = git
		? ((changesQuery.data as GitStatus | undefined) ?? EMPTY_GIT_STATUS)
		: EMPTY_GIT_STATUS;
	const loaded = (!git && canInit) || !changesQuery.isPending;
	const [menu, setMenu] = useState<MenuState | null>(null);
	const [busy, setBusy] = useState("");
	const [message, setMessage] = useState("");
	const messageRef = useRef("");
	const [operationError, setError] = useState("");
	const error =
		operationError ||
		(changesQuery.error instanceof Error
			? changesQuery.error.message
			: changesQuery.error
				? String(changesQuery.error)
				: "");
	const [notice, setNotice] = useState("");
	const [revertTarget, setRevertTarget] = useState<DiffEntry | null>(null);
	const load = useCallback(
		(action?: GitCommandType) => {
			if (
				git &&
				(action === "stage" || action === "stage_all" || action === "unstage")
			) {
				return invalidateGitIndexQueries(queryClient);
			}
			return queryClient.invalidateQueries(
				{
					queryKey: git ? queryKeys.git.all : queryKeys.diffs.all,
				},
				{ cancelRefetch: false },
			);
		},
		[git, queryClient],
	);
	useEffect(() => {
		if (!notice) return;
		const timeout = window.setTimeout(() => setNotice(""), 3000);
		return () => window.clearTimeout(timeout);
	}, [notice]);

	const request = useCallback(
		async (command: GitCommand) => {
			setBusy(command.type);
			setError("");
			setNotice("");
			try {
				const output = await runGitCommand(command);
				if (output) setNotice(output);
				await load(command.type);
				return true;
			} catch (e) {
				setError(e instanceof Error ? e.message : String(e));
				return false;
			} finally {
				setBusy("");
			}
		},
		[load],
	);

	const requestRevert = (diff: DiffEntry) => {
		setMenu(null);
		setRevertTarget(diff);
	};

	const updateMessage = (value: string) => {
		messageRef.current = value;
		setMessage(value);
	};

	const generateCommitMessage = async () => {
		const initial = messageRef.current;
		setBusy("commit-message");
		setError("");
		setNotice("");
		try {
			const generated = await generateGitCommitMessage();
			if (messageRef.current === initial) {
				updateMessage(generated);
			} else {
				setNotice(
					"Generated message was not inserted because the commit box changed.",
				);
			}
		} catch (error) {
			setError(error instanceof Error ? error.message : String(error));
		} finally {
			setBusy("");
		}
	};

	const confirmRevert = async () => {
		const diff = revertTarget;
		if (!diff) return;
		setBusy("revert");
		try {
			await revertDiff(diff.path);
			await load();
			setRevertTarget(null);
		} catch (e) {
			setError(e instanceof Error ? e.message : String(e));
		} finally {
			setBusy("");
		}
	};

	if (!git && canInit) {
		return <GitInitPrompt />;
	}

	if (git) {
		return (
			<>
				<GitChanges
					status={gitStatus}
					loaded={loaded}
					busy={busy}
					message={message}
					error={error}
					notice={notice}
					onMessage={updateMessage}
					onRequest={request}
					onOpenDiff={onOpenDiff}
					onOpenCompare={onOpenCompare}
					onOpenFile={onOpenFile}
					onCommit={async () => {
						if (await request({ type: "commit", message })) updateMessage("");
					}}
					onGenerateMessage={generateCommitMessage}
					onRevert={(file) => requestRevert(gitFileAsDiff(file))}
				/>
				<RevertDialog
					diff={revertTarget}
					git={git}
					onClose={() => setRevertTarget(null)}
					onConfirm={() => void confirmRevert()}
				/>
			</>
		);
	}

	return (
		<div className="relative flex h-full flex-col overflow-hidden bg-transparent">
			<div className="flex flex-1 flex-col overflow-y-auto py-2">
				{diffs.length === 0 && !error && <EmptyChanges loaded={loaded} />}
				{diffs.map((diff) => (
					<ChangeRow
						key={diff.path}
						path={diff.path}
						status={diffStatusLabel(diff)}
						disabled={busy !== ""}
						onClick={() => onOpenDiff?.(diff.path)}
						onDoubleClick={() => onOpenDiff?.(diff.path, undefined, "keep")}
						onContextMenu={(e) => {
							e.preventDefault();
							setMenu({ x: e.clientX, y: e.clientY, diff });
						}}
					/>
				))}
			</div>
			{error && <InlineMessage kind="error" text={error} />}
			{menu && (
				<FloatingMenu
					open
					onOpenChange={(open) => !open && setMenu(null)}
					reference={{ x: menu.x, y: menu.y }}
					label={`Actions for ${menu.diff.path}`}
					className="z-[100] min-w-[160px] bg-bg-elevated border border-border-subtle rounded-md shadow-2xl py-1 text-[12px]"
				>
					{menu.diff.status !== "deleted" && (
						<DiffMenuItem
							icon={<FileText size={12} />}
							label="Open"
							onClick={() => {
								setMenu(null);
								onOpenFile?.(menu.diff.path, "keep");
							}}
						/>
					)}
					<DiffMenuItem
						icon={<ClipboardCopy size={12} />}
						label="Copy"
						onClick={() => {
							setMenu(null);
							void navigator.clipboard.writeText(menu.diff.patch);
						}}
					/>
					<div className="my-1 border-t border-border-subtle" />
					<DiffMenuItem
						icon={<RotateCcw size={12} />}
						label="Revert"
						onClick={() => requestRevert(menu.diff)}
						danger
					/>
				</FloatingMenu>
			)}
			<RevertDialog
				diff={revertTarget}
				git={git}
				onClose={() => setRevertTarget(null)}
				onConfirm={() => void confirmRevert()}
			/>
		</div>
	);
}

function GitInitPrompt() {
	const queryClient = useQueryClient();
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");

	const init = async () => {
		setBusy(true);
		setError("");
		try {
			await initializeGitRepository();
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: queryKeys.capabilities }),
				queryClient.invalidateQueries({ queryKey: queryKeys.git.all }),
			]);
		} catch (e) {
			setError(e instanceof Error ? e.message : String(e));
		} finally {
			setBusy(false);
		}
	};

	return (
		<div className="flex h-full flex-col overflow-hidden bg-transparent">
			<div className="flex flex-1 flex-col items-center justify-center gap-3 overflow-y-auto px-5 text-center">
				<GitBranch size={18} className="text-fg-dim" />
				<p className="max-w-56 text-[11px] leading-5 text-fg-dim">
					This folder is not a Git repository. Initialize one to track, review,
					and commit changes.
				</p>
				<button
					type="button"
					disabled={busy}
					onClick={() => void init()}
					className="h-7 px-3 flex items-center gap-1.5 rounded-md bg-accent text-bg text-[10.5px] font-medium disabled:opacity-40"
				>
					{busy && <Loader2 size={11} className="animate-spin" />}
					Initialize Repository
				</button>
			</div>
			{error && <InlineMessage kind="error" text={error} />}
		</div>
	);
}

function RevertDialog({
	diff,
	git,
	onClose,
	onConfirm,
}: {
	diff: DiffEntry | null;
	git: boolean;
	onClose: () => void;
	onConfirm: () => void;
}) {
	const verb = diff
		? diff.status === "added"
			? "Delete"
			: diff.status === "deleted"
				? "Restore"
				: git
					? "Discard changes"
					: "Revert changes"
		: "Revert";
	return (
		<Dialog
			open={diff !== null}
			title={`${verb}?`}
			description={
				diff
					? `This operation affects “${diff.path}” and cannot be undone in Wingman.`
					: undefined
			}
			onClose={onClose}
		>
			<button type="button" className={dialogButtonClass} onClick={onClose}>
				Cancel
			</button>
			<button
				type="button"
				className={`${dialogButtonClass} border-danger/40 text-danger hover:bg-danger/10`}
				onClick={onConfirm}
			>
				{verb}
			</button>
		</Dialog>
	);
}

function GitChanges({
	status,
	loaded,
	busy,
	message,
	error,
	notice,
	onMessage,
	onRequest,
	onOpenDiff,
	onOpenCompare,
	onOpenFile,
	onCommit,
	onGenerateMessage,
	onRevert,
}: {
	status: GitStatus;
	loaded: boolean;
	busy: string;
	message: string;
	error: string;
	notice: string;
	onMessage: (value: string) => void;
	onRequest: (command: GitCommand) => Promise<boolean>;
	onOpenDiff?: (
		path: string,
		layer?: DiffLayer,
		disposition?: TabDisposition,
	) => void;
	onOpenCompare?: (
		base: string,
		head: string,
		mode: CompareMode,
		disposition?: TabDisposition,
	) => void;
	onOpenFile?: (path: string, disposition?: TabDisposition) => void;
	onCommit: () => Promise<void>;
	onGenerateMessage: () => Promise<void>;
	onRevert: (file: GitFileStatus) => void;
}) {
	const [menu, setMenu] = useState<GitMenuState | null>(null);
	const [historyOpen, setHistoryOpen] = useState(false);
	const [stackCommitAction, setStackCommitAction] = useState(false);
	const historyPanelRef = usePanelRef();
	const commitBoxRef = useRef<HTMLDivElement>(null);
	const commitMeasureRef = useRef<HTMLTextAreaElement>(null);
	const commitButtonRef = useRef<HTMLButtonElement>(null);
	const staged = useMemo(
		() => status.files.filter((file) => file.staged),
		[status.files],
	);
	const changed = useMemo(
		() => status.files.filter((file) => file.changed),
		[status.files],
	);
	const hasStaged = staged.length > 0;
	const disabled = busy !== "";
	const paths = (files: GitFileStatus[]) => [
		...new Set(files.flatMap(gitFilePaths)),
	];

	const openMenu = (
		event: React.MouseEvent,
		file: GitFileStatus,
		isStaged: boolean,
	) => {
		event.preventDefault();
		setMenu({
			x: event.clientX,
			y: event.clientY,
			file,
			staged: isStaged,
		});
	};
	const toggleHistory = useCallback(() => {
		const panel = historyPanelRef.current;
		if (!panel) return;
		if (panel.isCollapsed()) {
			setHistoryOpen(true);
			panel.resize("300px");
		} else {
			setHistoryOpen(false);
			panel.collapse();
		}
	}, [historyPanelRef]);
	const handleHistoryResize = useCallback(({ inPixels }: PanelSize) => {
		setHistoryOpen(inPixels > 33);
	}, []);
	const measureCommitLayout = useCallback(() => {
		const box = commitBoxRef.current;
		const measure = commitMeasureRef.current;
		const button = commitButtonRef.current;
		if (!message.trim() || !box || !measure || !button) {
			setStackCommitAction(false);
			return;
		}
		// Measure at the width the field would have beside the button. Keeping
		// that width stable avoids the layout flipping when the button moves.
		measure.style.width = `${Math.max(1, box.clientWidth - button.offsetWidth - 2)}px`;
		const style = window.getComputedStyle(measure);
		const singleLineHeight =
			Number.parseFloat(style.lineHeight) +
			Number.parseFloat(style.paddingTop) +
			Number.parseFloat(style.paddingBottom);
		setStackCommitAction(
			/[\r\n]/.test(message) || measure.scrollHeight > singleLineHeight + 1,
		);
	}, [message]);
	useLayoutEffect(() => {
		measureCommitLayout();
		const box = commitBoxRef.current;
		if (!box || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(measureCommitLayout);
		observer.observe(box);
		return () => observer.disconnect();
	}, [measureCommitLayout]);

	return (
		<Group
			id="git-changes-layout"
			orientation="vertical"
			className="h-full overflow-hidden bg-transparent"
		>
			<Panel
				id="git-current-changes"
				minSize="80px"
				className="min-h-0 overflow-hidden"
			>
				<div className="flex h-full min-h-0 flex-col overflow-hidden bg-transparent">
					<div className="relative h-9 px-2 flex items-center gap-1 shrink-0">
						<BranchPicker
							status={status}
							disabled={disabled}
							onRequest={onRequest}
							onCompare={onOpenCompare}
						/>
						<div className="flex-1" />
						<SyncButton
							label={status.behind ? `Pull ${status.behind}` : "Pull"}
							title={
								status.upstream
									? `Pull from ${status.upstream}`
									: "No upstream branch"
							}
							icon={<ArrowDownToLine size={12} />}
							disabled={disabled || !status.upstream}
							loading={busy === "pull"}
							onClick={() => void onRequest({ type: "pull" })}
						/>
						<SyncButton
							label={status.ahead ? `Push ${status.ahead}` : "Push"}
							title={
								status.upstream
									? `Push to ${status.upstream}`
									: "Publish branch"
							}
							icon={<ArrowUpFromLine size={12} />}
							disabled={disabled || !status.has_remote}
							loading={busy === "push"}
							onClick={() => void onRequest({ type: "push" })}
						/>
					</div>

					<div className="flex flex-1 flex-col overflow-y-auto py-1.5">
						{hasStaged ? (
							<>
								<ChangeGroup
									title="Staged Changes"
									files={staged}
									disabled={disabled}
									action={<Minus size={11} />}
									actionLabel="Unstage"
									onAll={() =>
										void onRequest({
											type: "unstage",
											paths: paths(staged),
										})
									}
									onFile={(file) =>
										void onRequest({
											type: "unstage",
											paths: gitFilePaths(file),
										})
									}
									onOpenDiff={onOpenDiff}
									onContextMenu={openMenu}
									staged
								/>
								<ChangeGroup
									title="Changes"
									files={changed}
									disabled={disabled}
									action={<Plus size={11} />}
									actionLabel="Stage"
									onAll={() => void onRequest({ type: "stage_all" })}
									onFile={(file) =>
										void onRequest({
											type: "stage",
											paths: gitFilePaths(file),
										})
									}
									onOpenDiff={onOpenDiff}
									onContextMenu={openMenu}
								/>
							</>
						) : changed.length > 0 ? (
							<ChangeGroup
								title="Changes"
								files={changed}
								disabled={disabled}
								action={<Plus size={11} />}
								actionLabel="Stage"
								onAll={() => void onRequest({ type: "stage_all" })}
								onFile={(file) =>
									void onRequest({
										type: "stage",
										paths: gitFilePaths(file),
									})
								}
								onOpenDiff={onOpenDiff}
								onContextMenu={openMenu}
							/>
						) : null}
						{status.files.length === 0 && !error && (
							<EmptyChanges loaded={loaded} />
						)}
					</div>

					{error && <InlineMessage kind="error" text={error} />}
					{notice && !error && <InlineMessage kind="notice" text={notice} />}
					{hasStaged && (
						<form
							onSubmit={(e) => {
								e.preventDefault();
								if (!disabled && message.trim()) void onCommit();
							}}
							className="px-2 py-2 border-t border-border-subtle bg-bg-surface/25 shrink-0"
						>
							<div className="mb-1.5 px-0.5 flex items-center text-[10px] text-fg-dim">
								<span>
									Commit {staged.length} staged{" "}
									{staged.length === 1 ? "file" : "files"}
								</span>
							</div>
							<div
								ref={commitBoxRef}
								className={`relative min-h-8 rounded-md border border-border-subtle bg-bg transition-colors focus-within:border-border ${
									stackCommitAction
										? "flex flex-col items-stretch"
										: "flex items-end"
								}`}
							>
								<textarea
									ref={commitMeasureRef}
									value={message}
									readOnly
									aria-hidden="true"
									tabIndex={-1}
									rows={1}
									className="pointer-events-none fixed left-0 top-0 -z-10 h-0 resize-none overflow-hidden px-2 py-1.5 text-[11.5px] leading-4 opacity-0"
								/>
								<textarea
									value={message}
									onChange={(e) => onMessage(e.target.value)}
									placeholder="Commit message…"
									aria-label="Commit message"
									rows={1}
									className={`field-sizing-content min-h-7 max-h-24 min-w-0 resize-none overflow-y-auto bg-transparent px-2 py-1.5 text-[11.5px] leading-4 text-fg placeholder:text-fg-dim outline-none ${
										stackCommitAction ? "w-full flex-none" : "flex-1"
									}`}
								/>
								{message.trim() ? (
									<button
										ref={commitButtonRef}
										type="submit"
										disabled={disabled}
										title="Commit staged changes"
										className={`mb-0.5 mr-0.5 flex h-7 shrink-0 items-center gap-1 rounded px-2 text-[10.5px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg disabled:cursor-not-allowed disabled:opacity-25 ${
											stackCommitAction ? "self-end" : ""
										}`}
									>
										{busy === "commit" ? (
											<Loader2 size={11} className="animate-spin" />
										) : (
											<GitCommitHorizontal size={11} />
										)}
										Commit
									</button>
								) : (
									<button
										type="button"
										disabled={disabled}
										onClick={() => void onGenerateMessage()}
										title="Generate commit message from staged changes"
										aria-label="Generate commit message"
										className="mb-0.5 mr-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg disabled:cursor-not-allowed disabled:opacity-30"
									>
										{busy === "commit-message" ? (
											<Loader2 size={11} className="animate-spin" />
										) : (
											<Sparkles size={11} />
										)}
									</button>
								)}
							</div>
						</form>
					)}
					{menu && (
						<FloatingMenu
							open
							onOpenChange={(open) => !open && setMenu(null)}
							reference={{ x: menu.x, y: menu.y }}
							label={`Actions for ${menu.file.path}`}
							className="z-[100] min-w-[170px] rounded-md border border-border-subtle bg-bg-elevated py-1 text-[12px] shadow-2xl"
						>
							<DiffMenuItem
								icon={<FileText size={12} />}
								label="Open Changes"
								onClick={() => {
									setMenu(null);
									onOpenDiff?.(
										menu.file.path,
										menu.file.conflict
											? undefined
											: menu.staged
												? "staged"
												: "unstaged",
										"keep",
									);
								}}
							/>
							{gitStatusLabel(menu.file, menu.staged) !== "D" && (
								<DiffMenuItem
									icon={<Code2 size={12} />}
									label="Open File"
									onClick={() => {
										setMenu(null);
										onOpenFile?.(menu.file.path, "keep");
									}}
								/>
							)}
							<DiffMenuItem
								icon={menu.staged ? <Minus size={11} /> : <Plus size={11} />}
								label={menu.staged ? "Unstage Changes" : "Stage Changes"}
								onClick={() => {
									const paths = gitFilePaths(menu.file);
									setMenu(null);
									void onRequest(
										menu.staged
											? { type: "unstage", paths }
											: { type: "stage", paths },
									);
								}}
							/>
							{!menu.staged && (
								<>
									<div className="my-1 border-t border-border-subtle" />
									<DiffMenuItem
										icon={<RotateCcw size={12} />}
										label="Discard Changes"
										danger
										onClick={() => {
											const file = menu.file;
											setMenu(null);
											onRevert(file);
										}}
									/>
								</>
							)}
						</FloatingMenu>
					)}
				</div>
			</Panel>
			{onOpenCompare && (
				<>
					<Separator
						aria-label="Resize Git history"
						disabled={!historyOpen}
						className={`relative z-20 -my-1.5 h-3 shrink-0 bg-transparent outline-none ${
							historyOpen ? "cursor-row-resize" : "pointer-events-none"
						}`}
					/>
					<Panel
						id="git-history"
						panelRef={historyPanelRef}
						defaultSize="32px"
						minSize="120px"
						maxSize="75%"
						collapsedSize="32px"
						collapsible
						groupResizeBehavior="preserve-pixel-size"
						onResize={handleHistoryResize}
						className="min-h-0 overflow-hidden"
					>
						<GitHistoryPanel
							open={historyOpen}
							disabled={disabled}
							onCompare={onOpenCompare}
							onToggle={toggleHistory}
						/>
					</Panel>
				</>
			)}
		</Group>
	);
}

function BranchPicker({
	status,
	disabled,
	onRequest,
	onCompare,
}: {
	status: GitStatus;
	disabled: boolean;
	onRequest: (command: GitCommand) => Promise<boolean>;
	onCompare?: (base: string, head: string, mode: CompareMode) => void;
}) {
	const buttonRef = useRef<HTMLButtonElement>(null);
	const searchRef = useRef<HTMLInputElement>(null);
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [creating, setCreating] = useState(false);
	const [newBranch, setNewBranch] = useState("");
	const queryClient = useQueryClient();
	const branchesQuery = useQuery({
		queryKey: queryKeys.git.branches,
		enabled: false,
		queryFn: ({ signal }) => getGitBranches(signal),
	});
	const refreshBranches = useMutation({
		mutationFn: fetchGitBranches,
		onSuccess: (branches) => {
			queryClient.setQueryData(queryKeys.git.branches, branches);
		},
	});
	const refetchBranches = branchesQuery.refetch;
	const refetchRemoteBranches = refreshBranches.mutate;
	const branches = branchesQuery.data?.branches ?? EMPTY_BRANCHES;
	const warning = branchesQuery.data?.warning ?? "";
	const branchError = refreshBranches.error ?? branchesQuery.error;
	const loadError = branchError
		? branchError instanceof Error
			? branchError.message
			: String(branchError)
		: "";
	const loading = branchesQuery.isFetching || refreshBranches.isPending;

	useEffect(() => {
		if (!open) return;
		void (async () => {
			await refetchBranches();
			refetchRemoteBranches();
		})();
		requestAnimationFrame(() => searchRef.current?.focus());
	}, [open, refetchBranches, refetchRemoteBranches]);

	const filtered = useMemo(() => {
		const needle = query.trim().toLowerCase();
		if (!needle) return branches;
		return branches.filter((branch) =>
			`${branch.remote ? `${branch.remote}/` : ""}${branch.name}`
				.toLowerCase()
				.includes(needle),
		);
	}, [branches, query]);
	const local = filtered.filter((branch) => !branch.remote);
	const remote = filtered.filter((branch) => branch.remote);

	const close = () => {
		setOpen(false);
		setQuery("");
		setCreating(false);
		setNewBranch("");
	};
	const checkout = async (branch: GitBranchInfo) => {
		if (branch.current) {
			close();
			return;
		}
		if (
			await onRequest({
				type: "checkout_branch",
				name: branch.name,
				...(branch.remote ? { remote: branch.remote } : {}),
			})
		) {
			close();
		}
	};
	const compare = (branch: GitBranchInfo) => {
		const ref = branch.remote ? `${branch.remote}/${branch.name}` : branch.name;
		onCompare?.(ref, ":worktree", "merge-base");
		close();
	};
	const create = async () => {
		const name = newBranch.trim();
		if (!name) return;
		if (await onRequest({ type: "create_branch", name })) close();
	};

	return (
		<div className="relative min-w-0">
			<button
				ref={buttonRef}
				type="button"
				disabled={disabled}
				onClick={() => setOpen((value) => !value)}
				title={status.upstream || status.branch || "Git branches"}
				aria-haspopup="dialog"
				aria-expanded={open}
				className="h-7 min-w-0 max-w-36 px-1.5 flex items-center gap-1 rounded text-[11px] text-fg-muted hover:text-fg hover:bg-bg-hover disabled:opacity-40 transition-colors"
			>
				<GitBranch size={12} className="text-fg-dim shrink-0" />
				<span className="truncate">{status.branch || "Git"}</span>
				<ChevronDown size={10} className="text-fg-dim shrink-0" />
			</button>

			<FloatingSurface
				open={open}
				onOpenChange={(nextOpen) => {
					if (nextOpen) setOpen(true);
					else close();
				}}
				reference={buttonRef.current}
				placement="bottom-start"
				role="dialog"
				label="Git branches"
				className="z-[100] w-[280px] overflow-hidden rounded-lg border border-border bg-bg-elevated shadow-2xl"
			>
				<div className="p-2 border-b border-border-subtle">
					<div className="h-7 flex items-center rounded-md border border-border-subtle bg-bg focus-within:border-border">
						<Search size={11} className="ml-2 text-fg-dim shrink-0" />
						<input
							ref={searchRef}
							autoCapitalize="none"
							autoComplete="off"
							autoCorrect="off"
							spellCheck={false}
							value={query}
							onChange={(event) => setQuery(event.target.value)}
							placeholder="Find a branch…"
							aria-label="Find a branch"
							className="min-w-0 flex-1 h-full px-1.5 bg-transparent outline-none text-[11px] text-fg placeholder:text-fg-dim"
						/>
						<button
							type="button"
							disabled={loading}
							onClick={() => void refetchRemoteBranches()}
							title="Fetch and refresh branches"
							className="w-7 h-full flex items-center justify-center text-fg-dim hover:text-fg disabled:opacity-40"
						>
							<RefreshCw size={11} className={loading ? "animate-spin" : ""} />
						</button>
					</div>
				</div>

				{warning && (
					<div
						className="px-3 py-2 text-[10px] leading-4 text-warning border-b border-border-subtle"
						title={warning}
					>
						Remote refresh failed. Showing cached branches.
					</div>
				)}
				{loadError && (
					<div className="px-3 py-2 text-[10px] leading-4 text-danger border-b border-border-subtle">
						{loadError}
					</div>
				)}

				<div className="max-h-56 overflow-y-auto py-1">
					{loading && branches.length === 0 ? (
						<div className="h-16 flex items-center justify-center text-fg-dim">
							<Loader2 size={13} className="animate-spin" />
						</div>
					) : (
						<>
							<BranchSection
								title="Local"
								branches={local}
								disabled={disabled}
								onSelect={checkout}
								onCompare={onCompare ? compare : undefined}
							/>
							<BranchSection
								title="Remote"
								branches={remote}
								disabled={disabled}
								onSelect={checkout}
								onCompare={onCompare ? compare : undefined}
							/>
							{filtered.length === 0 && !loadError && (
								<div className="px-3 py-5 text-center text-[10.5px] text-fg-dim">
									No matching branches
								</div>
							)}
						</>
					)}
				</div>

				<div className="p-2 border-t border-border-subtle bg-bg-surface/20">
					{creating ? (
						<form
							onSubmit={(event) => {
								event.preventDefault();
								if (!disabled) void create();
							}}
							className="flex items-center gap-1.5"
						>
							<input
								autoFocus
								autoCapitalize="none"
								autoComplete="off"
								autoCorrect="off"
								spellCheck={false}
								value={newBranch}
								onChange={(event) => setNewBranch(event.target.value)}
								placeholder="feature/name"
								aria-label="New branch name"
								className="h-7 min-w-0 flex-1 rounded-md border border-border-subtle bg-bg px-2 outline-none text-[11px] text-fg placeholder:text-fg-dim focus:border-border"
							/>
							<button
								type="submit"
								disabled={disabled || !newBranch.trim()}
								className="h-7 px-2 rounded-md bg-accent text-bg text-[10.5px] font-medium disabled:opacity-30"
							>
								Create
							</button>
						</form>
					) : (
						<button
							type="button"
							disabled={disabled}
							onClick={() => setCreating(true)}
							className="h-7 w-full px-2 flex items-center gap-1.5 rounded-md text-[10.5px] text-fg-muted hover:text-fg hover:bg-bg-hover disabled:opacity-35"
						>
							<Plus size={11} />
							Create new branch
						</button>
					)}
				</div>
			</FloatingSurface>
		</div>
	);
}

function BranchSection({
	title,
	branches,
	disabled,
	onSelect,
	onCompare,
}: {
	title: string;
	branches: GitBranchInfo[];
	disabled: boolean;
	onSelect: (branch: GitBranchInfo) => Promise<void>;
	onCompare?: (branch: GitBranchInfo) => void;
}) {
	if (branches.length === 0) return null;
	return (
		<div className="py-1">
			<div className="h-5 px-3 flex items-center text-[9px] font-medium uppercase tracking-wide text-fg-dim">
				{title}
			</div>
			{branches.map((branch) => {
				const label = branch.remote
					? `${branch.remote}/${branch.name}`
					: branch.name;
				return (
					<div
						key={`${branch.remote || "local"}:${branch.name}`}
						className="group flex h-8 items-center px-1.5 hover:bg-bg-hover"
					>
						<button
							type="button"
							disabled={disabled && !branch.current}
							onClick={() => void onSelect(branch)}
							className="flex h-full min-w-0 flex-1 items-center gap-2 rounded px-1.5 text-left text-[11px] text-fg-muted hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
						>
							<span className="w-3 shrink-0 text-accent">
								{branch.current && <Check size={11} />}
							</span>
							<span className="truncate">
								{branch.remote && (
									<span className="text-fg-dim">{branch.remote}/</span>
								)}
								{branch.name}
							</span>
						</button>
						{onCompare && (
							<button
								type="button"
								disabled={disabled}
								onClick={() => onCompare(branch)}
								title={`Compare ${label} with working tree`}
								aria-label={`Compare ${label} with working tree`}
								className="flex h-6 w-7 shrink-0 items-center justify-center rounded text-fg-dim hover:bg-bg-active hover:text-accent disabled:opacity-30"
							>
								<GitCompareArrows size={11} />
							</button>
						)}
					</div>
				);
			})}
		</div>
	);
}

function ChangeGroup({
	title,
	files,
	disabled,
	action,
	actionLabel,
	onAll,
	onFile,
	onOpenDiff,
	onContextMenu,
	staged = false,
}: {
	title: string;
	files: GitFileStatus[];
	disabled: boolean;
	action: React.ReactNode;
	actionLabel: string;
	onAll: () => void;
	onFile: (file: GitFileStatus) => void;
	onOpenDiff?: (
		path: string,
		layer?: DiffLayer,
		disposition?: TabDisposition,
	) => void;
	onContextMenu?: (
		event: React.MouseEvent,
		file: GitFileStatus,
		staged: boolean,
	) => void;
	staged?: boolean;
}) {
	if (files.length === 0) return null;
	return (
		<div className="shrink-0 pb-1">
			<div className="sticky top-0 z-10 h-7 px-3 flex items-center bg-bg-surface/80 text-[10.5px] font-medium text-fg-dim backdrop-blur-md">
				<span>{title}</span>
				<span className="ml-1.5 min-w-4 h-4 px-1 rounded-full bg-bg-active text-[9px] leading-4 text-center tabular-nums">
					{files.length}
				</span>
				<div className="flex-1" />
				<button
					type="button"
					disabled={disabled}
					onClick={onAll}
					title={`${actionLabel} all`}
					aria-label={`${actionLabel} all`}
					className="p-1 rounded hover:bg-bg-hover hover:text-fg disabled:opacity-30"
				>
					{action}
				</button>
			</div>
			<ChangeList
				files={files}
				disabled={disabled}
				action={action}
				actionLabel={actionLabel}
				onFile={onFile}
				onOpenDiff={onOpenDiff}
				onContextMenu={onContextMenu}
				staged={staged}
			/>
		</div>
	);
}

function ChangeList({
	files,
	disabled,
	action,
	actionLabel,
	onFile,
	onOpenDiff,
	onContextMenu,
	staged = false,
}: {
	files: GitFileStatus[];
	disabled: boolean;
	action: React.ReactNode;
	actionLabel: string;
	onFile: (file: GitFileStatus) => void;
	onOpenDiff?: (
		path: string,
		layer?: DiffLayer,
		disposition?: TabDisposition,
	) => void;
	onContextMenu?: (
		event: React.MouseEvent,
		file: GitFileStatus,
		staged: boolean,
	) => void;
	staged?: boolean;
}) {
	return files.map((file) => (
		<ChangeRow
			key={`${staged ? "staged" : "changed"}:${file.path}`}
			path={file.path}
			status={gitStatusLabel(file, staged)}
			conflict={file.conflict}
			disabled={disabled}
			onClick={() =>
				onOpenDiff?.(
					file.path,
					file.conflict ? undefined : staged ? "staged" : "unstaged",
				)
			}
			onDoubleClick={() =>
				onOpenDiff?.(
					file.path,
					file.conflict ? undefined : staged ? "staged" : "unstaged",
					"keep",
				)
			}
			action={action}
			actionLabel={actionLabel}
			onAction={() => onFile(file)}
			onContextMenu={(e) => {
				onContextMenu?.(e, file, staged);
			}}
		/>
	));
}

function ChangeRow({
	path,
	status,
	conflict,
	disabled,
	onClick,
	onDoubleClick,
	onContextMenu,
	action,
	actionLabel,
	onAction,
}: {
	path: string;
	status: string;
	conflict?: boolean;
	disabled: boolean;
	onClick: () => void;
	onDoubleClick?: () => void;
	onContextMenu?: (e: React.MouseEvent) => void;
	action?: React.ReactNode;
	actionLabel?: string;
	onAction?: () => void;
}) {
	const fileName = path.split("/").pop() || path;
	const dir = path.slice(0, path.length - fileName.length).replace(/\/$/, "");
	return (
		<div
			data-change-row={path}
			className={`group relative flex h-8 items-stretch text-[12px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg ${disabled ? "pointer-events-none opacity-50" : ""}`}
			onContextMenu={onContextMenu}
		>
			{action ? (
				<button
					type="button"
					disabled={disabled}
					onClick={() => onAction?.()}
					title={`${actionLabel} ${path}`}
					aria-label={`${actionLabel} ${path}`}
					className="group/action ml-2 flex w-5 shrink-0 items-center justify-center text-fg-dim transition-colors hover:text-fg focus-visible:text-fg"
				>
					<span
						data-change-status
						aria-hidden="true"
						className={`text-center text-[11px] font-bold group-hover:hidden group-focus/action:hidden ${statusColor(status, conflict)}`}
					>
						{status}
					</span>
					<span
						data-change-action
						aria-hidden="true"
						className="hidden items-center justify-center group-hover:flex group-focus/action:flex"
					>
						{action}
					</span>
				</button>
			) : (
				<span
					data-change-status
					className={`ml-3 flex w-3 shrink-0 items-center justify-center text-center text-[11px] font-bold ${statusColor(status, conflict)}`}
				>
					{status}
				</span>
			)}
			<button
				type="button"
				disabled={disabled}
				onClick={onClick}
				onDoubleClick={onDoubleClick}
				title={path}
				data-change-content
				className={`flex min-w-0 flex-1 items-center gap-2 pr-3 text-left ${action ? "pl-1" : "pl-2"}`}
			>
				<span className="truncate font-mono text-[12px]">{fileName}</span>
				{dir && (
					<span
						data-change-directory
						className="ml-auto truncate font-mono text-[11px] text-fg-dim"
					>
						{dir}
					</span>
				)}
			</button>
		</div>
	);
}

function SyncButton({
	label,
	title,
	icon,
	disabled,
	loading,
	onClick,
}: {
	label: string;
	title: string;
	icon: React.ReactNode;
	disabled: boolean;
	loading: boolean;
	onClick: () => void;
}) {
	return (
		<button
			type="button"
			title={title}
			aria-label={label}
			disabled={disabled}
			onClick={onClick}
			className="flex h-6 w-7 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg disabled:cursor-not-allowed disabled:opacity-30"
		>
			{loading ? <Loader2 size={12} className="animate-spin" /> : icon}
		</button>
	);
}

function EmptyChanges({ loaded }: { loaded: boolean }) {
	if (!loaded) {
		return (
			<PanelEmptyState
				icon={Loader2}
				iconClassName="animate-spin text-fg-dim"
				title="Loading…"
			/>
		);
	}
	return (
		<PanelEmptyState
			icon={CheckCircle2}
			iconClassName="text-success/70"
			title="Working tree clean"
			hint="New, modified, and deleted files appear here."
		/>
	);
}

function InlineMessage({
	kind,
	text,
}: {
	kind: "error" | "notice";
	text: string;
}) {
	return (
		<div
			className={`mx-2 mb-1 px-2 py-1.5 rounded text-[10.5px] whitespace-pre-wrap max-h-20 overflow-auto ${kind === "error" ? "bg-danger/10 text-danger" : "bg-bg-surface text-fg-dim"}`}
		>
			{text}
		</div>
	);
}

function gitFilePaths(file: GitFileStatus) {
	return file.original_path && file.original_path !== file.path
		? [file.path, file.original_path]
		: [file.path];
}

function gitFileAsDiff(file: GitFileStatus): DiffEntry {
	return {
		path: file.path,
		status:
			file.worktree_status === "?"
				? "added"
				: file.worktree_status === "D"
					? "deleted"
					: "modified",
		patch: "",
	};
}

function gitStatusLabel(file: GitFileStatus, staged: boolean) {
	if (file.conflict) return "!";
	const value = staged ? file.index_status : file.worktree_status;
	return value && value !== "." ? value : "M";
}

function diffStatusLabel(diff: DiffEntry) {
	return diff.status === "added" ? "A" : diff.status === "deleted" ? "D" : "M";
}

function statusColor(status: string, conflict?: boolean) {
	if (conflict) return "text-danger";
	if (status === "A" || status === "?") return "text-success";
	if (status === "D") return "text-danger";
	return "text-warning";
}

function DiffMenuItem({
	icon,
	label,
	onClick,
	danger,
}: {
	icon: React.ReactNode;
	label: string;
	onClick: () => void;
	danger?: boolean;
}) {
	return (
		<button
			type="button"
			role="menuitem"
			className={`w-full flex items-center gap-2 px-3 py-1.5 text-left hover:bg-bg-hover ${danger ? "text-danger" : "text-fg-muted hover:text-fg"}`}
			onClick={onClick}
		>
			<span className="w-3.5 flex items-center justify-center shrink-0">
				{icon}
			</span>
			<span>{label}</span>
		</button>
	);
}
