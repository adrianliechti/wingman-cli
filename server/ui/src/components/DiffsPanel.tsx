import {
	ArrowDownToLine,
	ArrowUpFromLine,
	Check,
	ChevronDown,
	ClipboardCopy,
	FileText,
	GitBranch,
	GitCommitHorizontal,
	Loader2,
	Minus,
	Plus,
	RefreshCw,
	RotateCcw,
	Search,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
	DiffEntry,
	DiffLayer,
	GitBranch as GitBranchInfo,
	GitBranches,
	GitFileStatus,
	GitStatus,
	ServerMessage,
} from "../types/protocol";

interface Props {
	sessionId: string;
	git?: boolean;
	onOpenDiff?: (path: string, layer?: DiffLayer) => void;
	onOpenFile?: (path: string) => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

interface MenuState {
	x: number;
	y: number;
	diff: DiffEntry;
}

const EMPTY_GIT_STATUS: GitStatus = {
	branch: "",
	ahead: 0,
	behind: 0,
	has_remote: false,
	files: [],
};

export function DiffsPanel({
	sessionId,
	git = false,
	onOpenDiff,
	onOpenFile,
	subscribe,
}: Props) {
	const [diffs, setDiffs] = useState<DiffEntry[]>([]);
	const [gitStatus, setGitStatus] = useState<GitStatus>(EMPTY_GIT_STATUS);
	const [loaded, setLoaded] = useState(false);
	const [menu, setMenu] = useState<MenuState | null>(null);
	const [busy, setBusy] = useState("");
	const [message, setMessage] = useState("");
	const [error, setError] = useState("");
	const [notice, setNotice] = useState("");
	const qs = sessionId ? `?session=${encodeURIComponent(sessionId)}` : "";

	const load = useCallback(async () => {
		try {
			const res = await fetch(git ? "/api/git/status" : `/api/diffs${qs}`);
			if (!res.ok) throw new Error(await responseError(res));
			if (git) setGitStatus((await res.json()) as GitStatus);
			else setDiffs((await res.json()) as DiffEntry[]);
			setError("");
		} catch (e) {
			if (git) setGitStatus(EMPTY_GIT_STATUS);
			else setDiffs([]);
			setError(e instanceof Error ? e.message : String(e));
		} finally {
			setLoaded(true);
		}
	}, [git, qs]);

	useEffect(() => {
		setLoaded(false);
		void load();
	}, [load]);
	useEffect(() => {
		if (!notice) return;
		const timeout = window.setTimeout(() => setNotice(""), 3000);
		return () => window.clearTimeout(timeout);
	}, [notice]);
	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "diffs_changed") void load();
		});
	}, [subscribe, load]);

	useEffect(() => {
		if (!menu) return;
		const close = () => setMenu(null);
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape") close();
		};
		document.addEventListener("mousedown", close);
		document.addEventListener("scroll", close, true);
		document.addEventListener("keydown", onKey);
		return () => {
			document.removeEventListener("mousedown", close);
			document.removeEventListener("scroll", close, true);
			document.removeEventListener("keydown", onKey);
		};
	}, [menu]);

	const request = useCallback(
		async (action: string, body?: unknown) => {
			setBusy(action);
			setError("");
			setNotice("");
			try {
				const res = await fetch(`/api/git/${action}`, {
					method: "POST",
					headers: body ? { "Content-Type": "application/json" } : undefined,
					body: body ? JSON.stringify(body) : undefined,
				});
				if (!res.ok) throw new Error(await responseError(res));
				if (res.headers.get("content-type")?.includes("application/json")) {
					const data = (await res.json()) as { output?: string };
					if (data.output) setNotice(data.output);
				}
				await load();
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

	const handleRevert = async (diff: DiffEntry) => {
		setMenu(null);
		const verb =
			diff.status === "added"
				? "Delete"
				: diff.status === "deleted"
					? "Restore"
					: git
						? "Discard unstaged changes to"
						: "Revert changes to";
		if (!confirm(`${verb} "${diff.path}"?`)) return;
		const params = new URLSearchParams({ path: diff.path });
		if (sessionId) params.set("session", sessionId);
		setBusy("revert");
		try {
			const res = await fetch(`/api/diffs/revert?${params.toString()}`, {
				method: "POST",
			});
			if (!res.ok) throw new Error(await responseError(res));
			await load();
		} catch (e) {
			setError(e instanceof Error ? e.message : String(e));
		} finally {
			setBusy("");
		}
	};

	if (git) {
		return (
			<GitChanges
				status={gitStatus}
				loaded={loaded}
				busy={busy}
				message={message}
				error={error}
				notice={notice}
				onMessage={setMessage}
				onRequest={request}
				onOpenDiff={onOpenDiff}
				onCommit={async () => {
					if (await request("commit", { message })) setMessage("");
				}}
				onRevert={(file) => void handleRevert(gitFileAsDiff(file))}
			/>
		);
	}

	return (
		<div className="flex flex-col h-full overflow-hidden bg-bg relative">
			<div className="overflow-y-auto flex-1 py-2">
				{diffs.length === 0 && !error && <EmptyChanges loaded={loaded} />}
				{diffs.map((diff) => (
					<ChangeRow
						key={diff.path}
						path={diff.path}
						status={diffStatusLabel(diff)}
						disabled={busy !== ""}
						onClick={() => onOpenDiff?.(diff.path)}
						onContextMenu={(e) => {
							e.preventDefault();
							setMenu({ x: e.clientX, y: e.clientY, diff });
						}}
					/>
				))}
			</div>
			{error && <InlineMessage kind="error" text={error} />}
			{menu && (
				<div
					className="fixed z-100 min-w-[160px] bg-bg-elevated border border-border-subtle rounded-md shadow-2xl py-1 text-[12px]"
					style={{ left: menu.x, top: menu.y }}
					onMouseDown={(e) => e.stopPropagation()}
				>
					{menu.diff.status !== "deleted" && (
						<DiffMenuItem
							icon={<FileText size={12} />}
							label="Open"
							onClick={() => {
								setMenu(null);
								onOpenFile?.(menu.diff.path);
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
						onClick={() => void handleRevert(menu.diff)}
						danger
					/>
				</div>
			)}
		</div>
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
	onCommit,
	onRevert,
}: {
	status: GitStatus;
	loaded: boolean;
	busy: string;
	message: string;
	error: string;
	notice: string;
	onMessage: (value: string) => void;
	onRequest: (action: string, body?: unknown) => Promise<boolean>;
	onOpenDiff?: (path: string, layer?: DiffLayer) => void;
	onCommit: () => Promise<void>;
	onRevert: (file: GitFileStatus) => void;
}) {
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

	return (
		<div className="flex flex-col h-full overflow-hidden bg-bg">
			<div className="relative h-9 px-2 flex items-center gap-1 shrink-0 border-b border-border-subtle bg-bg-surface/20">
				<BranchPicker
					status={status}
					disabled={disabled}
					onRequest={onRequest}
				/>
				{status.files.length > 0 && (
					<span
						className="min-w-4 h-4 px-1 rounded-full bg-bg-active text-[9px] leading-4 text-center text-fg-dim tabular-nums"
						title={`${status.files.length} changed ${status.files.length === 1 ? "file" : "files"}`}
					>
						{status.files.length}
					</span>
				)}
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
					onClick={() => void onRequest("pull")}
				/>
				<SyncButton
					label={status.ahead ? `Push ${status.ahead}` : "Push"}
					title={
						status.upstream ? `Push to ${status.upstream}` : "Publish branch"
					}
					icon={<ArrowUpFromLine size={12} />}
					disabled={disabled || !status.has_remote}
					loading={busy === "push"}
					onClick={() => void onRequest("push")}
				/>
			</div>

			<div className="overflow-y-auto flex-1 py-1.5">
				{hasStaged ? (
					<>
						<ChangeGroup
							title="Staged Changes"
							files={staged}
							disabled={disabled}
							action={<Minus size={11} />}
							actionLabel="Unstage"
							onAll={() => void onRequest("unstage", { paths: paths(staged) })}
							onFile={(file) =>
								void onRequest("unstage", { paths: gitFilePaths(file) })
							}
							onOpenDiff={onOpenDiff}
							staged
						/>
						<ChangeGroup
							title="Changes"
							files={changed}
							disabled={disabled}
							action={<Plus size={11} />}
							actionLabel="Stage"
							onAll={() => void onRequest("stage", { paths: paths(changed) })}
							onFile={(file) =>
								void onRequest("stage", { paths: gitFilePaths(file) })
							}
							onOpenDiff={onOpenDiff}
							onRevert={onRevert}
						/>
					</>
				) : changed.length > 0 ? (
					<>
						<div className="h-7 px-3 mb-0.5 flex items-center text-[10.5px] text-fg-dim">
							<span>
								{changed.length} changed{" "}
								{changed.length === 1 ? "file" : "files"}
							</span>
							<div className="flex-1" />
							<button
								type="button"
								disabled={disabled}
								onClick={() =>
									void onRequest("stage", { paths: paths(changed) })
								}
								className="h-6 px-2 flex items-center gap-1 rounded text-fg-dim hover:text-fg hover:bg-bg-hover disabled:opacity-30 transition-colors"
							>
								<Plus size={11} />
								Stage all
							</button>
						</div>
						<ChangeList
							files={changed}
							disabled={disabled}
							action={<Plus size={11} />}
							actionLabel="Stage"
							onFile={(file) =>
								void onRequest("stage", { paths: gitFilePaths(file) })
							}
							onOpenDiff={onOpenDiff}
							onRevert={onRevert}
						/>
					</>
				) : null}
				{status.files.length === 0 && !error && <EmptyChanges loaded={loaded} />}
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
					<div className="h-8 flex items-center rounded-md border border-border-subtle bg-bg focus-within:border-border transition-colors">
						<input
							value={message}
							onChange={(e) => onMessage(e.target.value)}
							placeholder="Commit message…"
							aria-label="Commit message"
							className="h-full min-w-0 flex-1 bg-transparent px-2 text-[11.5px] text-fg placeholder:text-fg-dim outline-none"
						/>
						<button
							type="submit"
							disabled={disabled || !message.trim()}
							title="Commit staged changes"
							className="h-7 mr-0.5 px-2 rounded flex items-center gap-1 text-[10.5px] text-fg-muted hover:text-fg hover:bg-bg-hover disabled:opacity-25 disabled:cursor-not-allowed transition-colors"
						>
							{busy === "commit" ? (
								<Loader2 size={11} className="animate-spin" />
							) : (
								<GitCommitHorizontal size={11} />
							)}
							Commit
						</button>
					</div>
				</form>
			)}
		</div>
	);
}

function BranchPicker({
	status,
	disabled,
	onRequest,
}: {
	status: GitStatus;
	disabled: boolean;
	onRequest: (action: string, body?: unknown) => Promise<boolean>;
}) {
	const root = useRef<HTMLDivElement>(null);
	const searchRef = useRef<HTMLInputElement>(null);
	const [open, setOpen] = useState(false);
	const [loading, setLoading] = useState(false);
	const [branches, setBranches] = useState<GitBranchInfo[]>([]);
	const [warning, setWarning] = useState("");
	const [loadError, setLoadError] = useState("");
	const [query, setQuery] = useState("");
	const [creating, setCreating] = useState(false);
	const [newBranch, setNewBranch] = useState("");

	const loadBranches = useCallback(async (refresh = true) => {
		setLoading(true);
		setLoadError("");
		try {
			const res = await fetch(
				`/api/git/branches?refresh=${refresh ? "1" : "0"}`,
			);
			if (!res.ok) throw new Error(await responseError(res));
			const data = (await res.json()) as GitBranches;
			setBranches(data.branches);
			setWarning(data.warning || "");
		} catch (e) {
			setLoadError(e instanceof Error ? e.message : String(e));
		} finally {
			setLoading(false);
		}
	}, []);

	useEffect(() => {
		if (!open) return;
		void (async () => {
			await loadBranches(false);
			await loadBranches(true);
		})();
		requestAnimationFrame(() => searchRef.current?.focus());
		const close = (event: MouseEvent) => {
			if (!root.current?.contains(event.target as Node)) setOpen(false);
		};
		const onKey = (event: KeyboardEvent) => {
			if (event.key === "Escape") setOpen(false);
		};
		document.addEventListener("mousedown", close);
		document.addEventListener("keydown", onKey);
		return () => {
			document.removeEventListener("mousedown", close);
			document.removeEventListener("keydown", onKey);
		};
	}, [open, loadBranches]);

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
			await onRequest("checkout", {
				name: branch.name,
				...(branch.remote ? { remote: branch.remote } : {}),
			})
		) {
			close();
		}
	};
	const create = async () => {
		const name = newBranch.trim();
		if (!name) return;
		if (await onRequest("branches", { name })) close();
	};

	return (
		<div ref={root} className="relative min-w-0">
			<button
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

			{open && (
				<div
					role="dialog"
					aria-label="Switch Git branch"
					className="absolute z-50 top-[31px] left-0 w-[280px] max-w-[calc(100vw-16px)] overflow-hidden rounded-lg border border-border bg-bg-elevated shadow-2xl"
				>
					<div className="p-2 border-b border-border-subtle">
						<div className="h-7 flex items-center rounded-md border border-border-subtle bg-bg focus-within:border-border">
							<Search size={11} className="ml-2 text-fg-dim shrink-0" />
							<input
								ref={searchRef}
								value={query}
								onChange={(event) => setQuery(event.target.value)}
								placeholder="Find a branch…"
								aria-label="Find a branch"
								className="min-w-0 flex-1 h-full px-1.5 bg-transparent outline-none text-[11px] text-fg placeholder:text-fg-dim"
							/>
							<button
								type="button"
								disabled={loading}
								onClick={() => void loadBranches(true)}
								title="Fetch and refresh branches"
								className="w-7 h-full flex items-center justify-center text-fg-dim hover:text-fg disabled:opacity-40"
							>
								<RefreshCw
									size={11}
									className={loading ? "animate-spin" : ""}
								/>
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
								/>
								<BranchSection
									title="Remote"
									branches={remote}
									disabled={disabled}
									onSelect={checkout}
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
				</div>
			)}
		</div>
	);
}

function BranchSection({
	title,
	branches,
	disabled,
	onSelect,
}: {
	title: string;
	branches: GitBranchInfo[];
	disabled: boolean;
	onSelect: (branch: GitBranchInfo) => Promise<void>;
}) {
	if (branches.length === 0) return null;
	return (
		<div className="py-1">
			<div className="h-5 px-3 flex items-center text-[9px] font-medium uppercase tracking-wide text-fg-dim">
				{title}
			</div>
			{branches.map((branch) => (
				<button
					key={`${branch.remote || "local"}:${branch.name}`}
					type="button"
					disabled={disabled && !branch.current}
					onClick={() => void onSelect(branch)}
					className="h-7 w-full px-3 flex items-center gap-2 text-left text-[11px] text-fg-muted hover:text-fg hover:bg-bg-hover disabled:opacity-40 disabled:cursor-not-allowed"
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
			))}
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
	onRevert,
	staged = false,
}: {
	title: string;
	files: GitFileStatus[];
	disabled: boolean;
	action: React.ReactNode;
	actionLabel: string;
	onAll: () => void;
	onFile: (file: GitFileStatus) => void;
	onOpenDiff?: (path: string, layer?: DiffLayer) => void;
	onRevert?: (file: GitFileStatus) => void;
	staged?: boolean;
}) {
	if (files.length === 0) return null;
	return (
		<div className="pb-1">
			<div className="h-7 px-3 flex items-center text-[10.5px] font-medium text-fg-dim">
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
				onRevert={onRevert}
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
	onRevert,
	staged = false,
}: {
	files: GitFileStatus[];
	disabled: boolean;
	action: React.ReactNode;
	actionLabel: string;
	onFile: (file: GitFileStatus) => void;
	onOpenDiff?: (path: string, layer?: DiffLayer) => void;
	onRevert?: (file: GitFileStatus) => void;
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
			action={action}
			actionLabel={actionLabel}
			onAction={() => onFile(file)}
			onContextMenu={(e) => {
				e.preventDefault();
				if (!staged) onRevert?.(file);
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
	onContextMenu?: (e: React.MouseEvent) => void;
	action?: React.ReactNode;
	actionLabel?: string;
	onAction?: () => void;
}) {
	const fileName = path.split("/").pop() || path;
	const dir = path.slice(0, path.length - fileName.length).replace(/\/$/, "");
	return (
		<div
			className={`group flex h-[26px] items-center gap-2 px-3 text-[12px] text-fg-muted hover:bg-bg-hover hover:text-fg transition-colors ${disabled ? "pointer-events-none opacity-50" : "cursor-pointer"}`}
			onClick={onClick}
			onContextMenu={onContextMenu}
			title={path}
		>
			<span
				className={`text-[10.5px] font-bold w-3 text-center shrink-0 ${statusColor(status, conflict)}`}
			>
				{status}
			</span>
			<span className="truncate font-mono text-[11.5px]">{fileName}</span>
			{dir && (
				<span className="text-fg-dim truncate font-mono text-[10.5px] ml-auto group-hover:hidden">
					{dir}
				</span>
			)}
			{action && (
				<button
					type="button"
					title={actionLabel}
					className="hidden group-hover:flex ml-auto w-5 h-5 items-center justify-center rounded text-fg-dim hover:text-fg hover:bg-bg-active"
					onClick={(e) => {
						e.stopPropagation();
						onAction?.();
					}}
				>
					{action}
				</button>
			)}
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
			disabled={disabled}
			onClick={onClick}
			className="h-6 px-1.5 flex items-center gap-1 rounded text-[10.5px] text-fg-dim hover:bg-bg-hover hover:text-fg disabled:opacity-30 disabled:cursor-not-allowed"
		>
			{loading ? <Loader2 size={12} className="animate-spin" /> : icon}
			<span>{label}</span>
		</button>
	);
}

function EmptyChanges({ loaded }: { loaded: boolean }) {
	return (
		<div className="px-3 py-7 text-[11px] text-fg-dim text-center">
			{loaded ? "Working tree clean" : "Loading…"}
		</div>
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

async function responseError(res: Response) {
	return (await res.text()).trim() || `${res.status} ${res.statusText}`;
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
			className={`w-full flex items-center gap-2 px-3 py-1.5 text-left hover:bg-bg-hover ${danger ? "text-red-400" : "text-fg-muted hover:text-fg"}`}
			onClick={onClick}
		>
			<span className="w-3.5 flex items-center justify-center shrink-0">
				{icon}
			</span>
			<span>{label}</span>
		</button>
	);
}
