import { defaultRangeExtractor, useVirtualizer } from "@tanstack/react-virtual";
import type { Range } from "@tanstack/react-virtual";
import {
	CaseSensitive,
	ChevronDown,
	ChevronRight,
	Ellipsis,
	FileCode2,
	Loader2,
	Regex,
	Replace,
	ReplaceAll,
	WholeWord,
	X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	streamWorkspaceSearch,
	type WorkspaceSearchFile,
	type WorkspaceSearchMatch,
	type WorkspaceSearchSummary,
} from "../api/search";
import type { ServerMessage } from "../types/protocol";
import type { TabDisposition } from "../types/tabs";
import type { WorkspaceEditEnvelope } from "../workspaceEdit";
import { getDeviconClass } from "../utils/fileIcons";
import {
	workspaceSearchEdit,
	workspaceSearchMatchID,
} from "../utils/workspaceSearch";
import { useToast } from "./ui/Feedback";

interface Props {
	active: boolean;
	focusKey: number;
	onClose: () => void;
	onOpenFile: (
		path: string,
		line: number,
		column: number,
		disposition?: TabDisposition,
	) => void;
	onApplyWorkspaceEdit: (
		envelope: WorkspaceEditEnvelope,
		label: string,
	) => Promise<boolean>;
	subscribe?: (handler: (message: ServerMessage) => void) => () => void;
}

const EMPTY_SUMMARY: WorkspaceSearchSummary = {
	files: 0,
	matches: 0,
	truncated: false,
};

export function SearchPanel({
	active,
	focusKey,
	onClose,
	onOpenFile,
	onApplyWorkspaceEdit,
	subscribe,
}: Props) {
	const toast = useToast();
	const findInputRef = useRef<HTMLInputElement>(null);
	const replaceInputRef = useRef<HTMLInputElement>(null);
	const requestRef = useRef<AbortController | null>(null);
	const requestSequenceRef = useRef(0);
	const searchIdentityRef = useRef("");
	const dismissedFilesRef = useRef(new Set<string>());
	const dismissedMatchesRef = useRef(new Set<string>());
	const [query, setQuery] = useState("");
	const [replacement, setReplacement] = useState("");
	const [replaceVisible, setReplaceVisible] = useState(false);
	const [caseSensitive, setCaseSensitive] = useState(false);
	const [wholeWord, setWholeWord] = useState(false);
	const [regex, setRegex] = useState(false);
	const [filtersVisible, setFiltersVisible] = useState(false);
	const [include, setInclude] = useState("");
	const [exclude, setExclude] = useState("");
	const [files, setFiles] = useState<WorkspaceSearchFile[]>([]);
	const [summary, setSummary] = useState<WorkspaceSearchSummary>(EMPTY_SUMMARY);
	const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
	const [loading, setLoading] = useState(false);
	const [applying, setApplying] = useState(false);
	const [error, setError] = useState("");
	const [refreshKey, setRefreshKey] = useState(0);

	useEffect(() => {
		if (!active) return;
		const frame = requestAnimationFrame(() => findInputRef.current?.focus());
		return () => cancelAnimationFrame(frame);
	}, [active, focusKey]);

	useEffect(() => {
		if (!active) {
			requestRef.current?.abort();
			setLoading(false);
			return;
		}
		const trimmedQuery = query;
		if (!trimmedQuery) {
			requestRef.current?.abort();
			searchIdentityRef.current = "";
			dismissedFilesRef.current.clear();
			dismissedMatchesRef.current.clear();
			setFiles([]);
			setSummary(EMPTY_SUMMARY);
			setError("");
			setLoading(false);
			return;
		}
		const searchIdentity = JSON.stringify([
			query,
			regex,
			caseSensitive,
			wholeWord,
			include,
			exclude,
			refreshKey,
		]);
		if (searchIdentityRef.current !== searchIdentity) {
			searchIdentityRef.current = searchIdentity;
			dismissedFilesRef.current.clear();
			dismissedMatchesRef.current.clear();
		}

		const sequence = ++requestSequenceRef.current;
		let controller: AbortController | undefined;
		const timer = window.setTimeout(() => {
			requestRef.current?.abort();
			const activeController = new AbortController();
			controller = activeController;
			requestRef.current = activeController;
			setFiles([]);
			setSummary(EMPTY_SUMMARY);
			setCollapsed(new Set());
			setError("");
			setLoading(true);

			void streamWorkspaceSearch(
				{
					query: trimmedQuery,
					replacement,
					regex,
					case_sensitive: caseSensitive,
					whole_word: wholeWord,
					include,
					exclude,
				},
				(file) => {
					if (
						activeController.signal.aborted ||
						requestSequenceRef.current !== sequence
					)
						return;
					if (dismissedFilesRef.current.has(file.path)) return;
					const visibleFile = {
						...file,
						matches: file.matches.filter(
							(match) =>
								!dismissedMatchesRef.current.has(
									workspaceSearchMatchID(file.path, match),
								),
						),
					};
					if (visibleFile.matches.length === 0) return;
					setFiles((current) => [...current, visibleFile]);
					setSummary((current) => ({
						...current,
						files: current.files + 1,
						matches: current.matches + visibleFile.matches.length,
					}));
				},
				activeController.signal,
			)
				.then((nextSummary) => {
					if (
						!activeController.signal.aborted &&
						requestSequenceRef.current === sequence
					) {
						setSummary((current) => ({
							...current,
							truncated: nextSummary.truncated,
						}));
					}
				})
				.catch((requestError) => {
					if (
						activeController.signal.aborted ||
						requestSequenceRef.current !== sequence
					)
						return;
					setError(
						requestError instanceof Error
							? requestError.message
							: String(requestError),
					);
				})
				.finally(() => {
					if (requestRef.current === activeController) {
						requestRef.current = null;
						setLoading(false);
					}
				});
		}, 180);

		return () => {
			window.clearTimeout(timer);
			controller?.abort();
		};
	}, [
		active,
		query,
		replacement,
		regex,
		caseSensitive,
		wholeWord,
		include,
		exclude,
		refreshKey,
	]);

	useEffect(() => {
		if (!active || !subscribe) return;
		let timer: number | undefined;
		const unsubscribe = subscribe((message) => {
			if (message.type !== "files_changed") return;
			window.clearTimeout(timer);
			timer = window.setTimeout(() => setRefreshKey((value) => value + 1), 180);
		});
		return () => {
			unsubscribe();
			window.clearTimeout(timer);
		};
	}, [active, subscribe]);

	const applyReplacement = useCallback(
		async (targetFiles: WorkspaceSearchFile[], matchCount: number) => {
			if (matchCount === 0 || applying || loading) return;
			const envelope = workspaceSearchEdit(targetFiles);
			setApplying(true);
			try {
				const applied = await onApplyWorkspaceEdit(
					envelope,
					`Replace ${matchCount} ${matchCount === 1 ? "match" : "matches"}?`,
				);
				if (applied) {
					toast({
						title: "Replacement applied",
						description: `${matchCount} ${matchCount === 1 ? "match" : "matches"} replaced.`,
						tone: "success",
					});
					setRefreshKey((value) => value + 1);
				}
			} finally {
				setApplying(false);
			}
		},
		[applying, loading, onApplyWorkspaceEdit, toast],
	);

	const dismissFile = useCallback((file: WorkspaceSearchFile) => {
		dismissedFilesRef.current.add(file.path);
		setFiles((current) => current.filter((candidate) => candidate !== file));
		setSummary((current) => ({
			...current,
			files: Math.max(0, current.files - 1),
			matches: Math.max(0, current.matches - file.matches.length),
		}));
	}, []);

	const dismissMatch = useCallback(
		(
			file: WorkspaceSearchFile,
			match: WorkspaceSearchFile["matches"][number],
		) => {
			const id = workspaceSearchMatchID(file.path, match);
			dismissedMatchesRef.current.add(id);
			setFiles((current) =>
				current.flatMap((candidate) => {
					if (candidate.path !== file.path) return [candidate];
					const matches = candidate.matches.filter(
						(candidateMatch) =>
							workspaceSearchMatchID(candidate.path, candidateMatch) !== id,
					);
					return matches.length > 0 ? [{ ...candidate, matches }] : [];
				}),
			);
			setSummary((current) => ({
				...current,
				files: Math.max(0, current.files - (file.matches.length === 1 ? 1 : 0)),
				matches: Math.max(0, current.matches - 1),
			}));
		},
		[],
	);

	const toggleReplace = useCallback(() => {
		const next = !replaceVisible;
		setReplaceVisible(next);
		requestAnimationFrame(() => {
			if (next) replaceInputRef.current?.focus();
			else findInputRef.current?.focus();
		});
	}, [replaceVisible]);

	return (
		<div
			className="flex h-full min-h-0 flex-col overflow-hidden bg-transparent"
			onKeyDown={(event) => {
				if (event.key === "Escape") {
					event.preventDefault();
					onClose();
				}
			}}
		>
			<div className="shrink-0 border-b border-border-subtle p-2">
				<div className="grid grid-cols-[minmax(0,1fr)_1.75rem] gap-x-1 gap-y-1">
					<button
						type="button"
						onClick={toggleReplace}
						title={replaceVisible ? "Hide replace" : "Show replace"}
						aria-label={replaceVisible ? "Hide replace" : "Show replace"}
						aria-expanded={replaceVisible}
						className="col-start-2 row-start-1 flex h-7 w-7 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg"
					>
						{replaceVisible ? (
							<ChevronDown size={14} />
						) : (
							<ChevronRight size={14} />
						)}
					</button>
					<div className="col-start-1 row-start-1 flex h-7 min-w-0 items-center rounded-md border border-border-subtle bg-bg-input focus-within:border-border-strong">
						<input
							ref={findInputRef}
							value={query}
							onChange={(event) => setQuery(event.target.value)}
							placeholder="Search"
							aria-label="Search workspace"
							spellCheck={false}
							className="h-full min-w-0 flex-1 bg-transparent px-2 font-mono text-[11px] text-fg placeholder:text-fg-dim"
						/>
						{loading && (
							<Loader2
								size={11}
								className="mr-1 shrink-0 animate-spin text-fg-dim"
								aria-label="Searching"
							/>
						)}
						<SearchOption
							active={caseSensitive}
							title="Match case"
							onClick={() => setCaseSensitive((value) => !value)}
						>
							<CaseSensitive size={13} />
						</SearchOption>
						<SearchOption
							active={wholeWord}
							title="Match whole word"
							onClick={() => setWholeWord((value) => !value)}
						>
							<WholeWord size={13} />
						</SearchOption>
						<SearchOption
							active={regex}
							title="Use regular expression"
							onClick={() => setRegex((value) => !value)}
						>
							<Regex size={13} />
						</SearchOption>
					</div>
					{replaceVisible && (
						<>
							<div className="col-start-1 row-start-2 flex h-7 min-w-0 items-center rounded-md border border-border-subtle bg-bg-input focus-within:border-border-strong">
								<input
									ref={replaceInputRef}
									value={replacement}
									onChange={(event) => setReplacement(event.target.value)}
									placeholder="Replace"
									aria-label="Replace with"
									spellCheck={false}
									className="h-full min-w-0 flex-1 bg-transparent px-2 font-mono text-[11px] text-fg placeholder:text-fg-dim"
								/>
							</div>
							<button
								type="button"
								disabled={summary.matches === 0 || loading || applying}
								onClick={() => void applyReplacement(files, summary.matches)}
								title={`Replace all ${summary.matches} ${summary.matches === 1 ? "match" : "matches"}`}
								aria-label="Replace all matches"
								className="col-start-2 row-start-2 flex h-7 w-7 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg disabled:cursor-default disabled:opacity-35"
							>
								{applying ? (
									<Loader2 size={14} className="animate-spin" />
								) : (
									<ReplaceAll size={14} />
								)}
							</button>
						</>
					)}
					<button
						type="button"
						onClick={() => setFiltersVisible((value) => !value)}
						title={filtersVisible ? "Hide file filters" : "Show file filters"}
						aria-label={
							filtersVisible ? "Hide file filters" : "Show file filters"
						}
						aria-expanded={filtersVisible}
						className={`col-start-2 ${replaceVisible ? "row-start-3" : "row-start-2"} -mt-0.5 flex h-4 w-7 items-center justify-center rounded transition-colors ${
							filtersVisible
								? "bg-bg-hover text-fg"
								: include || exclude
									? "text-accent hover:bg-bg-hover"
									: "text-fg-dim hover:bg-bg-hover hover:text-fg"
						}`}
					>
						<Ellipsis size={14} />
					</button>

					{filtersVisible && (
						<div className="col-span-2 col-start-1 space-y-1">
							<label className="block">
								<span className="mb-0.5 block text-[10px] text-fg-dim">
									Files to include
								</span>
								<input
									value={include}
									onChange={(event) => setInclude(event.target.value)}
									placeholder="e.g. *.ts, src/**"
									spellCheck={false}
									className="h-7 w-full min-w-0 rounded-md border border-border-subtle bg-bg-input px-2 font-mono text-[10px] text-fg placeholder:text-fg-dim focus:border-border-strong"
								/>
							</label>
							<label className="block">
								<span className="mb-0.5 block text-[10px] text-fg-dim">
									Files to exclude
								</span>
								<input
									value={exclude}
									onChange={(event) => setExclude(event.target.value)}
									placeholder="e.g. dist/**, **/*.test.*"
									spellCheck={false}
									className="h-7 w-full min-w-0 rounded-md border border-border-subtle bg-bg-input px-2 font-mono text-[10px] text-fg placeholder:text-fg-dim focus:border-border-strong"
								/>
							</label>
						</div>
					)}
				</div>
			</div>

			{query && (
				<div className="flex h-7 shrink-0 items-center border-b border-border-subtle px-3 text-[10px] text-fg-dim">
					{summary.matches} {summary.matches === 1 ? "result" : "results"} in{" "}
					{summary.files} {summary.files === 1 ? "file" : "files"}
					{summary.truncated ? " (limit reached)" : ""}
				</div>
			)}

			{error ? (
				<div className="mx-2 my-1 rounded bg-danger/5 px-2 py-1.5 text-[10px] text-danger/80">
					{error}
				</div>
			) : query && !loading && files.length === 0 ? (
				<div className="px-3 py-6 text-center text-[11px] text-fg-dim">
					No results found
				</div>
			) : (
				<VirtualSearchResults
					files={files}
					collapsed={collapsed}
					replaceVisible={replaceVisible}
					loading={loading}
					applying={applying}
					onToggleFile={(path) =>
						setCollapsed((current) => {
							const next = new Set(current);
							if (next.has(path)) next.delete(path);
							else next.add(path);
							return next;
						})
					}
					onDismissFile={dismissFile}
					onDismissMatch={dismissMatch}
					onApplyReplacement={(targetFiles, matchCount) =>
						void applyReplacement(targetFiles, matchCount)
					}
					onOpenFile={onOpenFile}
				/>
			)}
		</div>
	);
}

type SearchVirtualItem =
	| {
			kind: "file";
			key: string;
			file: WorkspaceSearchFile;
	  }
	| {
			kind: "match";
			key: string;
			file: WorkspaceSearchFile;
			match: WorkspaceSearchMatch;
	  };

function VirtualSearchResults({
	files,
	collapsed,
	replaceVisible,
	loading,
	applying,
	onToggleFile,
	onDismissFile,
	onDismissMatch,
	onApplyReplacement,
	onOpenFile,
}: {
	files: WorkspaceSearchFile[];
	collapsed: Set<string>;
	replaceVisible: boolean;
	loading: boolean;
	applying: boolean;
	onToggleFile: (path: string) => void;
	onDismissFile: (file: WorkspaceSearchFile) => void;
	onDismissMatch: (
		file: WorkspaceSearchFile,
		match: WorkspaceSearchMatch,
	) => void;
	onApplyReplacement: (
		files: WorkspaceSearchFile[],
		matchCount: number,
	) => void;
	onOpenFile: Props["onOpenFile"];
}) {
	const scrollRef = useRef<HTMLDivElement>(null);
	const items = useMemo<SearchVirtualItem[]>(() => {
		const result: SearchVirtualItem[] = [];
		for (const file of files) {
			result.push({ kind: "file", key: `file:${file.path}`, file });
			if (collapsed.has(file.path)) continue;
			for (const match of file.matches) {
				result.push({
					kind: "match",
					key: `match:${workspaceSearchMatchID(file.path, match)}`,
					file,
					match,
				});
			}
		}
		return result;
	}, [collapsed, files]);
	const stickyIndexes = useMemo(
		() => items.flatMap((item, index) => (item.kind === "file" ? [index] : [])),
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
	const virtualizer = useVirtualizer({
		count: items.length,
		getScrollElement: () => scrollRef.current,
		getItemKey: (index) => items[index].key,
		estimateSize: (index) =>
			items[index].kind === "file" || !replaceVisible ? 28 : 40,
		overscan: 12,
		rangeExtractor,
	});

	useEffect(() => {
		virtualizer.measure();
	}, [replaceVisible, virtualizer]);

	return (
		<div
			ref={scrollRef}
			className="min-h-0 flex-1 overflow-y-auto"
			data-virtual-search-results
		>
			<div
				className="relative w-full"
				style={{ height: virtualizer.getTotalSize() }}
			>
				{virtualizer.getVirtualItems().map((virtualRow) => {
					const item = items[virtualRow.index];
					const file = item.file;
					if (item.kind === "file") {
						const isCollapsed = collapsed.has(file.path);
						const activeSticky =
							virtualRow.index === activeStickyIndexRef.current;
						const pinned =
							activeSticky &&
							(virtualizer.scrollOffset ?? 0) > virtualRow.start;
						const parts = file.path.split("/");
						const name = parts.pop() || file.path;
						const directory = parts.join("/");
						const icon = getDeviconClass(name);
						return (
							<div
								key={virtualRow.key}
								data-index={virtualRow.index}
								ref={virtualizer.measureElement}
								className={`group flex h-7 w-full items-center bg-bg px-1.5 text-[11px] hover:bg-bg-hover ${
									activeSticky
										? `sticky top-0 z-20 ${pinned ? "shadow-[0_1px_0_var(--color-border),0_3px_8px_rgba(0,0,0,0.15)]" : ""}`
										: "absolute left-0 top-0"
								}`}
								style={
									activeSticky
										? undefined
										: { transform: `translateY(${virtualRow.start}px)` }
								}
							>
								<button
									type="button"
									onClick={() => onToggleFile(file.path)}
									className="flex h-full min-w-0 flex-1 items-center gap-1.5 rounded px-1 text-left text-fg-muted hover:text-fg"
									title={file.path}
								>
									{isCollapsed ? (
										<ChevronRight size={11} className="shrink-0 text-fg-dim" />
									) : (
										<ChevronDown size={11} className="shrink-0 text-fg-dim" />
									)}
									{icon ? (
										<i className={`${icon} shrink-0 text-[13px] text-fg-dim`} />
									) : (
										<FileCode2 size={12} className="shrink-0 text-fg-dim" />
									)}
									<span className="truncate font-medium">{name}</span>
									{directory && (
										<span className="truncate text-[9px] text-fg-dim">
											{directory}
										</span>
									)}
								</button>
								{replaceVisible && (
									<button
										type="button"
										disabled={loading || applying}
										onClick={() =>
											onApplyReplacement([file], file.matches.length)
										}
										title={`Replace all matches in ${file.path}`}
										aria-label={`Replace all matches in ${file.path}`}
										className="flex h-7 w-6 shrink-0 items-center justify-center rounded text-fg-dim opacity-0 hover:bg-bg-active hover:text-fg group-hover:opacity-100 focus-visible:opacity-100 disabled:opacity-30"
									>
										<ReplaceAll size={11} />
									</button>
								)}
								<button
									type="button"
									onClick={() => onDismissFile(file)}
									title={`Remove ${file.path} from results`}
									aria-label={`Remove ${file.path} from results`}
									className="flex h-7 w-6 shrink-0 items-center justify-center rounded text-fg-dim opacity-0 hover:bg-bg-active hover:text-fg group-hover:opacity-100 focus-visible:opacity-100"
								>
									<X size={11} />
								</button>
							</div>
						);
					}

					const match = item.match;
					const previewBefore = match.before.trimStart();
					const previewAfter = match.after.trimEnd();
					return (
						<div
							key={virtualRow.key}
							data-index={virtualRow.index}
							ref={virtualizer.measureElement}
							className={`group absolute left-0 top-0 flex w-full items-start px-1.5 hover:bg-bg-hover ${replaceVisible ? "h-10" : "h-7"}`}
							style={{ transform: `translateY(${virtualRow.start}px)` }}
						>
							<button
								type="button"
								onClick={() =>
									onOpenFile(file.path, match.line, match.column, "preview")
								}
								onDoubleClick={() =>
									onOpenFile(file.path, match.line, match.column, "keep")
								}
								className="h-full min-w-0 flex-1 py-1 pr-1 text-left font-mono text-[10px] leading-4 text-fg-muted"
								title={`${file.path}:${match.line}:${match.column}`}
							>
								<div className="flex min-w-0">
									<span className="mr-2 w-7 shrink-0 text-right tabular-nums text-fg-dim">
										{match.line}
									</span>
									<span className="min-w-0 truncate whitespace-pre">
										{previewBefore}
										<span className="rounded-sm bg-accent-muted/60 text-fg">
											{match.text}
										</span>
										{previewAfter}
									</span>
								</div>
								{replaceVisible && (
									<div className="flex min-w-0 text-fg-dim">
										<span className="mr-2 w-7 shrink-0 text-right">→</span>
										<span className="truncate whitespace-pre text-fg-muted">
											{match.replacement || "(empty)"}
										</span>
									</div>
								)}
							</button>
							<div className="flex h-7 shrink-0 items-center">
								{replaceVisible && (
									<button
										type="button"
										disabled={loading || applying}
										onClick={() =>
											onApplyReplacement([{ ...file, matches: [match] }], 1)
										}
										title={`Replace match on line ${match.line}`}
										aria-label={`Replace match on line ${match.line}`}
										className="flex h-7 w-6 shrink-0 items-center justify-center rounded text-fg-dim opacity-0 hover:bg-bg-active hover:text-fg group-hover:opacity-100 focus-visible:opacity-100 disabled:opacity-30"
									>
										<Replace size={11} />
									</button>
								)}
								<button
									type="button"
									onClick={() => onDismissMatch(file, match)}
									title={`Remove match on line ${match.line} from results`}
									aria-label={`Remove match on line ${match.line} from results`}
									className="flex h-7 w-6 shrink-0 items-center justify-center rounded text-fg-dim opacity-0 hover:bg-bg-active hover:text-fg group-hover:opacity-100 focus-visible:opacity-100"
								>
									<X size={11} />
								</button>
							</div>
						</div>
					);
				})}
			</div>
		</div>
	);
}

function SearchOption({
	active,
	title,
	onClick,
	children,
}: {
	active: boolean;
	title: string;
	onClick: () => void;
	children: React.ReactNode;
}) {
	return (
		<button
			type="button"
			aria-pressed={active}
			title={title}
			onClick={onClick}
			className={`mr-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded transition-colors ${
				active
					? "bg-bg-active text-fg"
					: "text-fg-dim hover:bg-bg-hover hover:text-fg"
			}`}
		>
			{children}
		</button>
	);
}
