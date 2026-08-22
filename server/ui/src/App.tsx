import {
	useMutation,
	useQueries,
	useQuery,
	useQueryClient,
} from "@tanstack/react-query";
import {
	Bug,
	Compass,
	Code2,
	Eye,
	FileText,
	GitCompare,
	Globe2,
	Lightbulb,
	Loader2,
	Monitor,
	MonitorPlay,
	PanelLeftOpen,
	PanelRightOpen,
	Plus,
	RefreshCw,
	Search,
	Sparkles,
	SquareTerminal,
	Wrench,
} from "lucide-react";
import {
	type CSSProperties,
	type ErrorInfo,
	type ReactNode,
	useCallback,
	useEffect,
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
import { agentQueries, setCurrentAgent } from "./api/agents";
import {
	getInspectAvailability,
	type ManagedToolsStatus,
} from "./api/capabilities";
import {
	controlDebug,
	getDebugSession,
	getDebugState,
	type DebugSession,
} from "./api/debug";
import { createWorkspaceFile } from "./api/files";
import { queryKeys } from "./api/query";
import {
	createSession,
	deleteSession,
	loadSession,
	sessionQueries,
	setMode,
	type ModeOption,
	type ModeState,
	type SessionInfo,
} from "./api/sessions";
import { setEditorTabCompletion } from "./api/settings";
import {
	deleteTerminal,
	getTerminal,
	startTerminal,
	terminalQueries,
} from "./api/terminals";
import { chooseWorkspaceFolder, replaceWorkspace } from "./api/workspaces";
import { ChatPanel } from "./components/ChatPanel";
import {
	PaneDropZones,
	type PaneZone,
	TabStrip,
	type TabStripItem,
} from "./components/TabStrip";
import {
	type CenterTab,
	chatTabId,
	draftChatTab,
	moveTab,
	paneOf,
	type PaneSide,
	placeCenterTab,
	syncDebugTab,
	withSessionFallback,
} from "./mainLayout";
import {
	CommandPalette,
	type PaletteAction,
	type PaletteSkill,
} from "./components/CommandPalette";
import { DiffsPanel } from "./components/DiffsPanel";
import {
	DebugLauncher,
	type DebugLauncherSeed,
} from "./components/DebugLauncher";
import { DebugTab } from "./components/DebugTab";
import { DebugOutputTab } from "./components/DebugOutputTab";
import { DebugToolbar, type DebugOperation } from "./components/DebugToolbar";
import { DiffTab } from "./components/DiffTab";
import { CompareTab } from "./components/CompareTab";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ErrorPanel } from "./components/ErrorScreen";
import { FileTab } from "./components/FileTab";
import { InsightsTab } from "./components/insights/InsightsTab";
import { ProblemsPanel } from "./components/ProblemsPanel";
import { TasksPanel } from "./components/TasksPanel";
import { TaskTab } from "./components/TaskTab";
import { TerminalView } from "./components/TerminalView";
import { WorkspaceFilesPanel } from "./components/WorkspaceFilesPanel";
import {
	Dialog,
	dialogButtonClass,
	dialogPrimaryButtonClass,
	useToast,
} from "./components/ui/Feedback";
import { FloatingMenu, FloatingSurface } from "./components/ui/Floating";
import { AgentPicker, BUILTIN_AGENT_ID } from "./components/AgentPicker";
import { AgentSessions } from "./components/AgentSessions";
import { useCapabilities } from "./hooks/useCapabilities";
import { useOpenDocuments } from "./hooks/useOpenDocuments";
import { useServerQueryInvalidation } from "./hooks/useServerQueryInvalidation";
import { type ChatEntry, useWebSocket } from "./hooks/useWebSocket";
import type {
	CompareMode,
	DiffLayer,
	ShellEntry,
	TaskEntry,
	TerminalEntry,
	TurnInputIntent,
} from "./types/protocol";
import type { TabDisposition } from "./types/tabs";
import {
	defaultFileView,
	type FileView,
	textPreviewKind,
} from "./utils/filePreview";
import {
	summarizeWorkspaceEdit,
	type WorkspaceEditEnvelope,
	type WorkspaceEditSummary,
} from "./workspaceEdit";

type WorkspaceTab = "changes" | "files" | "inspect" | "agents";
type DebugContentView = "output" | "terminal";
type CloseRequest = { kind: "file" | "terminal"; tab: CenterTab } | null;
type SaveConflictRequest = { path: string; closeTabId?: string } | null;
type WorkspaceEditRequest = {
	envelope: WorkspaceEditEnvelope;
	label: string;
	summary: WorkspaceEditSummary;
	resolve: (applied: boolean) => void;
	applying: boolean;
} | null;
type SessionDeleteRequest = { id: string; title: string } | null;
type FilePathRequest =
	| { kind: "new"; path: string; submitting: boolean; error?: string }
	| {
			kind: "save-as";
			path: string;
			sourcePath: string;
			sourceTabId: string;
			content: string;
			submitting: boolean;
			error?: string;
	  };
const LEFT_PANEL_DEFAULT_SIZE = 240;
const LEFT_PANEL_MIN_SIZE = 240;
const LEFT_PANEL_MAX_SIZE = 360;
const RIGHT_PANEL_MIN_SIZE = 280;
const RIGHT_PANEL_DEFAULT_SIZE = RIGHT_PANEL_MIN_SIZE;
const RIGHT_PANEL_MAX_SIZE = 480;
const CENTER_PANEL_MIN_SIZE = 320;
const DEBUG_DETAILS_MIN_SIZE = 240;
const DEBUG_DETAILS_MAX_SIZE = 480;

const EMPTY_ENTRIES: never[] = [];
const EMPTY_MODES: ModeOption[] = [];
const EMPTY_SHELLS: ShellEntry[] = [];
const EMPTY_USAGE = {
	inputTokens: 0,
	cachedTokens: 0,
	outputTokens: 0,
	lastInputTokens: 0,
	contextWindow: 0,
};

const TERMINAL_SHORTCUT = /Mac|iPhone|iPad/.test(navigator.platform)
	? "⌃⌥T"
	: "Ctrl+Alt+T";
const TERMINAL_SHELL_MENU_HINT = /Mac|iPhone|iPad/.test(navigator.platform)
	? "Option-click"
	: "Alt-click";

function terminalShellName(name: string): string {
	const knownNames: Record<string, string> = {
		bash: "Bash",
		cmd: "Command Prompt",
		fish: "Fish",
		nu: "Nushell",
		powershell: "PowerShell",
		pwsh: "PowerShell",
		zsh: "Zsh",
	};
	return knownNames[name.toLowerCase()] ?? name;
}

function moveWorkspacePath(path: string, from: string, to: string): string {
	return path === from || path.startsWith(`${from}/`)
		? `${to}${path.slice(from.length)}`
		: path;
}

function shortRevision(revision: string): string {
	if (revision === ":worktree") return "Working tree";
	if (revision === ":empty") return "Empty tree";
	const last = revision.split("/").pop() || revision;
	return /^[0-9a-f]{12,}$/i.test(last) ? last.slice(0, 7) : last;
}

function moveWorkspaceTab(tab: CenterTab, from: string, to: string): CenterTab {
	if (!tab.path) return tab;
	const path = moveWorkspacePath(tab.path, from, to);
	if (path === tab.path) return tab;
	const name = path.split("/").pop() || path;
	return {
		...tab,
		id:
			tab.type === "file"
				? `file:${path}`
				: tab.type === "diff"
					? `diff:${tab.diffLayer ?? "combined"}:${path}`
					: tab.id,
		path,
		label: tab.diffLayer ? `${name} · ${tab.diffLayer}` : name,
	};
}

export default function App() {
	const {
		connected,
		sessions,
		toolProgress,
		hasSession,
		sendChat,
		cancel,
		removeQueued,
		updateQueued,
		resumeQueue,
		clearQueue,
		dismissPending,
		dismissError,
		respondPrompt,
		removeSession,
		clearSessions,
		subscribe,
	} = useWebSocket();
	useServerQueryInvalidation(subscribe, connected);
	const queryClient = useQueryClient();
	const toast = useToast();
	const createSessionMutation = useMutation({
		mutationFn: createSession,
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all });
		},
	}).mutateAsync;
	const {
		documents,
		dirtyPaths,
		openDocument,
		openCreatedDocument,
		updateDraft,
		saveDocument,
		discardDocument,
		reloadDocument,
		closeDocument,
		moveDocuments,
		applyWorkspaceEdit,
	} = useOpenDocuments(subscribe);
	const capabilities = useCapabilities();
	const showChanges = !!(capabilities?.diffs || capabilities?.git_init);
	const { inspect: showInspect, debug: showDebug } =
		getInspectAvailability(capabilities);
	const showAgents = capabilities?.tasks ?? false;
	const showTerminal = capabilities?.terminal ?? false;
	const tabAvailable = capabilities?.tab ?? false;
	const tabEnabled =
		tabAvailable && (capabilities?.["editor.tab.completion"] ?? false);
	const toggleEditorTabCompletion = useCallback(async () => {
		try {
			await setEditorTabCompletion(!tabEnabled);
			void queryClient.invalidateQueries({
				queryKey: queryKeys.capabilities,
			});
			toast({
				title: `editor.tab.completion ${tabEnabled ? "disabled" : "enabled"}`,
				description: tabEnabled
					? undefined
					: "Completions use model requests while you type.",
			});
		} catch (error) {
			toast({
				title: "Could not change editor.tab.completion",
				description: String(error),
				tone: "error",
			});
		}
	}, [queryClient, tabEnabled, toast]);
	const [requestedWorkspaceTab, setRequestedWorkspaceTab] =
		useState<WorkspaceTab>("files");
	const [problemsRefreshKey, setProblemsRefreshKey] = useState(0);
	const [workspaceSearching, setWorkspaceSearching] = useState(false);
	const [searchFocusKey, setSearchFocusKey] = useState(0);
	const [leftPanelCollapsed, setLeftPanelCollapsed] = useState(true);
	const [rightPanelCollapsed, setRightPanelCollapsed] = useState(false);
	const appRef = useRef<HTMLDivElement>(null);
	const leftPanelWidthRef = useRef(LEFT_PANEL_DEFAULT_SIZE);
	const rightPanelDefaultWidth = RIGHT_PANEL_DEFAULT_SIZE;
	const rightPanelWidthRef = useRef(rightPanelDefaultWidth);
	const leftPanelRef = usePanelRef();
	const rightPanelRef = usePanelRef();
	const debugDetailsPanelRef = usePanelRef();
	const debugDetailsWidthRef = useRef(DEBUG_DETAILS_MIN_SIZE);
	const terminalShells =
		useQuery({
			...terminalQueries.shells(),
			enabled: showTerminal,
		}).data ?? EMPTY_SHELLS;
	const terminalsQuery = useQuery({
		...terminalQueries.list(),
		enabled: showTerminal,
	});
	const terminalCreatingRef = useRef(false);
	const debugTerminalIDsRef = useRef(new Set<string>());
	const exitedDebugTerminalIDsRef = useRef(new Set<string>());

	const [tabs, setTabs] = useState<CenterTab[]>([draftChatTab()]);
	const [activeTabId, setActiveTabId] = useState(chatTabId(""));
	const [dragTabId, setDragTabId] = useState<string | null>(null);
	const [leftActiveId, setLeftActiveId] = useState(chatTabId(""));
	const [rightActiveId, setRightActiveId] = useState("");
	const activePaneRef = useRef<"right" | undefined>(undefined);
	const [fileViews, setFileViews] = useState<Record<string, FileView>>({});
	const [currentSessionId, setCurrentSessionId] = useState("");
	const [paletteOpen, setPaletteOpen] = useState(false);
	const [composerSeed, setComposerSeed] = useState<{
		text: string;
		nonce: number;
	} | null>(null);
	const [closeRequest, setCloseRequest] = useState<CloseRequest>(null);
	const [saveConflict, setSaveConflict] = useState<SaveConflictRequest>(null);
	const [workspaceEditRequest, setWorkspaceEditRequest] =
		useState<WorkspaceEditRequest>(null);
	const [sessionDelete, setSessionDelete] =
		useState<SessionDeleteRequest>(null);
	const [filePathRequest, setFilePathRequest] =
		useState<FilePathRequest | null>(null);
	const [openFolderRequest, setOpenFolderRequest] = useState(false);
	const [workspaceSwitching, setWorkspaceSwitching] = useState(false);
	const [debugLauncher, setDebugLauncher] = useState<DebugLauncherSeed | null>(
		null,
	);
	const [debugContentView, setDebugContentView] =
		useState<DebugContentView>("output");
	const [debugDetailsVisible, setDebugDetailsVisible] = useState(true);
	const [debugSession, setDebugSession] = useState<DebugSession>();
	const debugSessionRef = useRef<DebugSession | undefined>(undefined);
	const followedDebugStopRef = useRef("");
	const [debugControlBusy, setDebugControlBusy] = useState(false);

	const runWorkspaceEdit = useCallback(
		async (envelope: WorkspaceEditEnvelope, label: string) => {
			const result = await applyWorkspaceEdit(envelope);
			if (!result.ok) {
				toast({
					title: `${label} failed`,
					description: result.error,
					tone: "error",
				});
			}
			return result.ok;
		},
		[applyWorkspaceEdit, toast],
	);

	const requestWorkspaceEdit = useCallback(
		(envelope: WorkspaceEditEnvelope, label: string): Promise<boolean> => {
			let summary: WorkspaceEditSummary;
			try {
				summary = summarizeWorkspaceEdit(envelope);
			} catch (error) {
				toast({
					title: `${label} failed`,
					description: error instanceof Error ? error.message : String(error),
					tone: "error",
				});
				return Promise.resolve(false);
			}
			if (!summary.requiresConfirmation) {
				return runWorkspaceEdit(envelope, label);
			}
			return new Promise<boolean>((resolve) => {
				setWorkspaceEditRequest({
					envelope,
					label,
					summary,
					resolve,
					applying: false,
				});
			});
		},
		[runWorkspaceEdit, toast],
	);

	const closeWorkspaceEditPreview = useCallback(() => {
		workspaceEditRequest?.resolve(false);
		setWorkspaceEditRequest(null);
	}, [workspaceEditRequest]);

	const confirmWorkspaceEdit = useCallback(async () => {
		const request = workspaceEditRequest;
		if (!request || request.applying) return;
		setWorkspaceEditRequest({ ...request, applying: true });
		const applied = await runWorkspaceEdit(request.envelope, request.label);
		request.resolve(applied);
		setWorkspaceEditRequest((current) =>
			current?.resolve === request.resolve ? null : current,
		);
	}, [runWorkspaceEdit, workspaceEditRequest]);

	const createTerminal = useCallback(
		async (shell?: string) => {
			if (terminalCreatingRef.current) return;
			terminalCreatingRef.current = true;
			try {
				const entry = await startTerminal(shell);
				queryClient.setQueryData<TerminalEntry[]>(
					queryKeys.terminals.list,
					(current = []) =>
						current.some((terminal) => terminal.id === entry.id)
							? current
							: [...current, entry],
				);
				const tab: CenterTab = {
					id: `terminal:${entry.id}`,
					type: "terminal",
					label: entry.title,
					terminalId: entry.id,
					pane: activePaneRef.current,
				};
				setTabs((prev) =>
					prev.some((item) => item.id === tab.id) ? prev : [...prev, tab],
				);
				setActiveTabId(tab.id);
			} catch (error) {
				toast({
					title: "Could not create terminal",
					description: error instanceof Error ? error.message : String(error),
					tone: "error",
				});
			} finally {
				terminalCreatingRef.current = false;
			}
		},
		[queryClient, toast],
	);

	useEffect(() => {
		if (!showTerminal || !terminalsQuery.data) return;
		const entries = terminalsQuery.data;
		const ids = new Set(entries.map((entry) => entry.id));
		setTabs((prev) => {
			const next = prev.filter(
				(tab) => tab.type !== "terminal" || ids.has(tab.terminalId ?? ""),
			);
			const known = new Set(
				next.flatMap((tab) => (tab.terminalId ? [tab.terminalId] : [])),
			);
			for (const id of debugTerminalIDsRef.current) known.add(id);
			for (const entry of entries) {
				if (known.has(entry.id)) continue;
				next.push({
					id: `terminal:${entry.id}`,
					type: "terminal",
					label: entry.title,
					terminalId: entry.id,
				});
			}
			return next;
		});
	}, [showTerminal, terminalsQuery.data]);

	useEffect(() => {
		const onKey = (e: KeyboardEvent) => {
			// Ctrl/Cmd+P matches the TUI command center (and suppresses the
			// browser print dialog); Cmd+K stays as the web-app convention.
			if (
				(e.metaKey || e.ctrlKey) &&
				["k", "p"].includes(e.key.toLowerCase())
			) {
				e.preventDefault();
				setPaletteOpen((o) => !o);
				return;
			}
			// Match on e.code: with Alt held macOS reports the composed character
			// in e.key, and Backquote sits on a dead key in several EU layouts.
			if (
				e.ctrlKey &&
				!e.metaKey &&
				((e.altKey && e.code === "KeyT") ||
					(!e.altKey && e.code === "Backquote"))
			) {
				e.preventDefault();
				void createTerminal();
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [createTerminal]);

	const activeTab = tabs.find((t) => t.id === activeTabId) || tabs[0];
	const leftTabs = tabs.filter((tab) => paneOf(tab) === "left");
	const rightTabs = leftTabs.length
		? tabs.filter((tab) => paneOf(tab) === "right")
		: [];
	const rightTab =
		(activeTab.pane === "right"
			? rightTabs.find((tab) => tab.id === activeTab.id)
			: undefined) ??
		rightTabs.find((tab) => tab.id === rightActiveId) ??
		rightTabs[0];
	const leftPool = leftTabs.length ? leftTabs : tabs;
	const leftTab =
		(activeTab.pane !== "right"
			? leftPool.find((tab) => tab.id === activeTab.id)
			: undefined) ??
		leftPool.find((tab) => tab.id === leftActiveId) ??
		leftPool[0] ??
		activeTab;
	const activeDocument =
		activeTab.type === "file" && activeTab.path
			? documents[activeTab.path]
			: undefined;
	const canSaveFile = !!(
		activeTab.type === "file" &&
		activeTab.path &&
		activeDocument?.file &&
		!activeDocument.external &&
		!activeDocument.file.binary &&
		!activeDocument.saving
	);
	const activePreviewKind =
		activeTab.type === "file" ? textPreviewKind(activeTab.path ?? "") : null;
	const activeFileView =
		fileViews[activeTab.id] ?? defaultFileView(activeTab.path ?? "");
	const previewToggleLabel =
		activeFileView === "preview"
			? "Show code editor"
			: activePreviewKind === "html"
				? "Show browser preview"
				: activePreviewKind === "markdown"
					? "Show Markdown preview"
					: activePreviewKind === "svg"
						? "Show image preview"
						: activePreviewKind === "mermaid"
							? "Show diagram preview"
							: activePreviewKind === "csv" || activePreviewKind === "tsv"
								? "Show table preview"
								: "Show data preview";
	const sessionId =
		activeTab.type === "chat" ? (activeTab.sessionId ?? "") : currentSessionId;
	const workspaceTab =
		(requestedWorkspaceTab === "changes" && !showChanges) ||
		(requestedWorkspaceTab === "agents" && !showAgents) ||
		(requestedWorkspaceTab === "inspect" && !showInspect)
			? "files"
			: requestedWorkspaceTab;

	const activeSession = sessionId ? sessions[sessionId] : undefined;
	const entries = activeSession?.entries ?? EMPTY_ENTRIES;
	const phase = activeSession?.phase ?? "idle";
	const usage = activeSession?.usage ?? EMPTY_USAGE;

	const agentId = useQuery(agentQueries.current()).data?.agent ?? "";
	const [switchingAgent, setSwitchingAgent] = useState<string | null>(null);

	const deepLinkRef = useRef<string | null>(null);

	useEffect(() => {
		if (!agentId) return;
		if (!sessionId && deepLinkRef.current) return;
		const path = sessionId
			? `/${encodeURIComponent(agentId)}/${encodeURIComponent(sessionId)}`
			: "/";
		if (window.location.pathname !== path) {
			window.history.replaceState(null, "", path);
		}
	}, [agentId, sessionId]);

	const streamEstimate =
		phase !== "idle" ? estimateStreamingTokens(entries) : 0;
	const outputTokens = usage.outputTokens + streamEstimate;

	const activateTab = useCallback((tab: CenterTab) => {
		setActiveTabId(tab.id);
		if (tab.type === "chat") setCurrentSessionId(tab.sessionId ?? "");
	}, []);
	const keepTab = useCallback((id: string) => {
		setTabs((current) =>
			current.map((tab) =>
				tab.id === id && tab.preview ? { ...tab, preview: undefined } : tab,
			),
		);
	}, []);
	const showCenterTab = useCallback(
		(candidate: CenterTab, disposition: TabDisposition) => {
			const placement = placeCenterTab(
				tabs,
				{ ...candidate, pane: activeTab.pane },
				disposition,
				dirtyPaths,
			);
			if (placement.replaced?.type === "file" && placement.replaced.path) {
				closeDocument(placement.replaced.path);
			}
			if (placement.replaced) {
				setFileViews((current) => {
					if (!(placement.replaced!.id in current)) return current;
					const next = { ...current };
					delete next[placement.replaced!.id];
					return next;
				});
			}
			setTabs(withSessionFallback(placement.tabs));
			activateTab(candidate);
		},
		[activateTab, activeTab.pane, closeDocument, dirtyPaths, tabs],
	);

	const openChatTab = useCallback(
		(sid: string, disposition: TabDisposition = "keep", adoptDraft = false) => {
			const existing = tabs.find(
				(t) => t.type === "chat" && t.sessionId === sid,
			);
			if (existing) {
				showCenterTab(existing, disposition);
				return;
			}

			// Allocating a session for the draft must retain its component key so
			// unsent composer state survives. Browsing history never adopts it.
			const draft = adoptDraft
				? tabs.find((t) => t.type === "chat" && !t.sessionId)
				: undefined;
			const tab: CenterTab = {
				id: draft ? draft.id : chatTabId(sid),
				type: "chat",
				label: "Session",
				sessionId: sid,
				pane: draft?.pane,
			};
			if (draft) {
				setTabs((current) =>
					current.map((candidate) =>
						candidate.id === draft.id ? tab : candidate,
					),
				);
				activateTab(tab);
				return;
			}
			showCenterTab(tab, disposition);
		},
		[activateTab, showCenterTab, tabs],
	);

	const openFile = useCallback(
		(
			path: string,
			line?: number,
			column?: number,
			external?: boolean,
			disposition: TabDisposition = "preview",
		) => {
			openDocument(path, external ?? false);
			const existing = tabs.find((t) => t.type === "file" && t.path === path);
			if (existing) {
				if (line) {
					setTabs((prev) =>
						prev.map((t) =>
							t.id === existing.id
								? {
										...t,
										line,
										column,
										navigationKey: (t.navigationKey ?? 0) + 1,
									}
								: t,
						),
					);
				}
				if (disposition === "keep") keepTab(existing.id);
				activateTab(existing);
				return;
			}
			const label = path.split("/").pop() || path;
			const tab: CenterTab = {
				id: `file:${path}`,
				type: "file",
				label,
				path,
				line,
				column,
				navigationKey: line ? 1 : undefined,
				external: external || undefined,
			};
			showCenterTab(tab, disposition);
		},
		[activateTab, keepTab, openDocument, showCenterTab, tabs],
	);

	const handleFileMove = useCallback(
		(from: string, to: string) => {
			moveDocuments(from, to);
			const movedTabs = new Map(
				tabs.map((tab) => [tab.id, moveWorkspaceTab(tab, from, to)]),
			);
			setTabs((current) =>
				current.map((tab) => moveWorkspaceTab(tab, from, to)),
			);
			setActiveTabId((current) => movedTabs.get(current)?.id ?? current);
			setLeftActiveId((current) => movedTabs.get(current)?.id ?? current);
			setRightActiveId((current) => movedTabs.get(current)?.id ?? current);
			setFileViews((current) => {
				let changed = false;
				const next = { ...current };
				for (const [oldID, tab] of movedTabs) {
					if (tab.id === oldID || !(oldID in next)) continue;
					next[tab.id] = next[oldID];
					delete next[oldID];
					changed = true;
				}
				return changed ? next : current;
			});
		},
		[moveDocuments, tabs],
	);

	const openTask = useCallback(
		(task: TaskEntry) => {
			if (!sessionId) return;
			const id = `task:${sessionId}:${task.id}`;
			setTabs((prev) =>
				prev.some((t) => t.id === id)
					? prev
					: [
							...prev,
							{
								id,
								type: "task" as const,
								label: task.description,
								sessionId,
								taskId: task.id,
								pane: activePaneRef.current,
							},
						],
			);
			setActiveTabId(id);
		},
		[sessionId],
	);

	const openDiff = useCallback(
		(
			path: string,
			layer?: DiffLayer,
			disposition: TabDisposition = "preview",
		) => {
			const existing = tabs.find(
				(t) => t.type === "diff" && t.path === path && t.diffLayer === layer,
			);
			if (existing) {
				if (disposition === "keep") keepTab(existing.id);
				activateTab(existing);
				return;
			}
			const fileName = path.split("/").pop() || path;
			const label = layer ? `${fileName} · ${layer}` : fileName;
			const tab: CenterTab = {
				id: `diff:${layer ?? "combined"}:${path}`,
				type: "diff",
				label,
				path,
				diffLayer: layer,
			};
			showCenterTab(tab, disposition);
		},
		[activateTab, keepTab, showCenterTab, tabs],
	);

	const openCompare = useCallback(
		(
			base: string,
			head: string,
			mode: CompareMode,
			disposition: TabDisposition = "keep",
		) => {
			const id = `compare:${mode}:${base}:${head}`;
			const label = `${shortRevision(base)} → ${shortRevision(head)}`;
			showCenterTab(
				{
					id,
					type: "compare",
					label,
					compareBase: base,
					compareHead: head,
					compareMode: mode,
				},
				disposition,
			);
		},
		[showCenterTab],
	);

	const openInsightsTab = useCallback(() => {
		showCenterTab({ id: "graph", type: "graph", label: "Insights" }, "keep");
	}, [showCenterTab]);
	const openDebugLauncher = useCallback((seed: DebugLauncherSeed) => {
		setDebugLauncher({ ...seed });
	}, []);
	const applyDebugSession = useCallback(
		(session?: DebugSession) => {
			const current = debugSessionRef.current;
			if (
				session &&
				current?.session_id === session.session_id &&
				session.state_version < current.state_version
			)
				return;
			debugSessionRef.current = session;
			setDebugSession(session);
			if (session?.terminal_id)
				debugTerminalIDsRef.current.add(session.terminal_id);
			const active = !!session && session.state !== "terminated";
			const terminalId =
				active &&
				session.terminal_id &&
				!exitedDebugTerminalIDsRef.current.has(session.terminal_id)
					? session.terminal_id
					: undefined;
			setTabs((current) =>
				syncDebugTab(current, terminalId, active, activePaneRef.current),
			);
			if (!terminalId)
				setDebugContentView((view) => (view === "terminal" ? "output" : view));
			queryClient.setQueryData(queryKeys.debug.session, { session });
		},
		[queryClient],
	);
	const debugStateQuery = useQuery({
		queryKey: queryKeys.debug.state,
		enabled: showDebug,
		staleTime: 0,
		queryFn: ({ signal }) => getDebugState(undefined, signal),
		refetchInterval: (current) => {
			const session = current.state.data?.session;
			if (!session || session.state === "terminated") return false;
			return session.state === "running" ? 500 : 1_250;
		},
	});

	useEffect(() => {
		if (!showDebug) {
			applyDebugSession(undefined);
			return;
		}
		if (debugStateQuery.data) {
			applyDebugSession(debugStateQuery.data.session);
		}
	}, [applyDebugSession, debugStateQuery.data, showDebug]);

	useEffect(() => {
		const state = debugStateQuery.data;
		const session = state?.session;
		const frame = state?.frame;
		if (
			session?.state !== "stopped" ||
			debugSession?.session_id !== session.session_id ||
			debugSession.state_version !== session.state_version ||
			!frame?.source?.path ||
			frame.line < 1
		) {
			return;
		}
		const stopKey = `${session.session_id}:${session.state_version}`;
		if (followedDebugStopRef.current === stopKey) return;
		followedDebugStopRef.current = stopKey;
		openFile(frame.source.path, frame.line, Math.max(1, frame.column));
	}, [debugSession, debugStateQuery.data, openFile]);

	const handleDebugControl = useCallback(
		async (operation: DebugOperation) => {
			const session = debugSession;
			if (!session || session.state === "terminated" || debugControlBusy)
				return;
			setDebugControlBusy(true);
			try {
				const result = await controlDebug(
					operation,
					session.session_id,
					session.stop?.thread_id,
				);
				if (result.session) {
					applyDebugSession(result.session);
					void queryClient.invalidateQueries({
						queryKey: queryKeys.debug.state,
						exact: true,
					});
				}
			} catch (error) {
				toast({
					title: `Could not ${debugOperationLabel(operation)}`,
					description: error instanceof Error ? error.message : String(error),
					tone: "error",
				});
			} finally {
				setDebugControlBusy(false);
			}
		},
		[applyDebugSession, debugControlBusy, debugSession, queryClient, toast],
	);

	const closeTabNow = useCallback(
		(id: string) => {
			const idx = tabs.findIndex((t) => t.id === id);
			if (idx < 0) return;
			const closing = tabs[idx];
			if (!isClosableTab(closing)) return;
			if (closing.type === "file" && closing.path) closeDocument(closing.path);
			setTabs((prev) =>
				withSessionFallback(prev.filter((tab) => tab.id !== id)),
			);
			setFileViews((prev) => {
				if (!(id in prev)) return prev;
				const next = { ...prev };
				delete next[id];
				return next;
			});
			if (activeTabId === id) {
				const remaining = tabs.filter((t) => t.id !== id);
				const paneIdx = tabs
					.filter((t) => paneOf(t) === paneOf(closing))
					.findIndex((t) => t.id === id);
				const paneTabs = remaining.filter((t) => paneOf(t) === paneOf(closing));
				const fallback = remaining.some((t) => t.type === "chat")
					? (paneTabs[Math.min(paneIdx, paneTabs.length - 1)] ??
						remaining[Math.min(idx, remaining.length - 1)] ??
						draftChatTab())
					: draftChatTab();
				activateTab(fallback);
			}
		},
		[tabs, activeTabId, activateTab, closeDocument],
	);

	const closeTerminal = useCallback(
		async (tab: CenterTab) => {
			if (!tab.terminalId) return;
			try {
				await deleteTerminal(tab.terminalId);
				setCloseRequest((current) =>
					current?.kind === "terminal" && current.tab.id === tab.id
						? null
						: current,
				);
				queryClient.setQueryData<TerminalEntry[]>(
					queryKeys.terminals.list,
					(current = []) =>
						current.filter((entry) => entry.id !== tab.terminalId),
				);
				closeTabNow(tab.id);
			} catch (error) {
				toast({
					title: "Could not close terminal",
					description: error instanceof Error ? error.message : String(error),
					tone: "error",
				});
			}
		},
		[closeTabNow, queryClient, toast],
	);

	const saveFile = useCallback(
		async (path: string, closeTabId?: string) => {
			const result = await saveDocument(path);
			if (result.conflict) {
				if (closeTabId) setCloseRequest(null);
				setSaveConflict({ path, closeTabId });
				return result;
			}
			if (!result.ok) {
				toast({
					title: "Could not save file",
					description: result.error,
					tone: "error",
				});
				return result;
			}
			if (closeTabId) {
				setCloseRequest(null);
				closeTabNow(closeTabId);
			}
			return result;
		},
		[closeTabNow, saveDocument, toast],
	);

	const submitFilePath = useCallback(async () => {
		const request = filePathRequest;
		if (!request || request.submitting) return;
		const path = request.path.trim().replaceAll("\\", "/");
		if (!path) {
			setFilePathRequest({
				...request,
				error: "Enter a workspace-relative path.",
			});
			return;
		}

		if (request.kind === "save-as" && path === request.sourcePath) {
			const result = await saveFile(request.sourcePath);
			if (result?.ok || result?.conflict) setFilePathRequest(null);
			return;
		}

		setFilePathRequest({
			...request,
			path,
			submitting: true,
			error: undefined,
		});
		try {
			const content = request.kind === "save-as" ? request.content : "";
			const created = await createWorkspaceFile(path, { content });
			if (!created) throw new Error("The created file response was empty.");
			openCreatedDocument(created);

			if (request.kind === "new") {
				openFile(created.path, undefined, undefined, undefined, "keep");
			} else {
				const nextId = `file:${created.path}`;
				closeDocument(request.sourcePath);
				setTabs((current) =>
					current.map((tab) =>
						tab.id === request.sourceTabId
							? {
									...tab,
									id: nextId,
									label: created.path.split("/").pop() || created.path,
									path: created.path,
									external: undefined,
									preview: undefined,
								}
							: tab,
					),
				);
				setLeftActiveId((current) =>
					current === request.sourceTabId ? nextId : current,
				);
				setRightActiveId((current) =>
					current === request.sourceTabId ? nextId : current,
				);
				setFileViews((current) => {
					if (!(request.sourceTabId in current)) return current;
					const next = { ...current, [nextId]: current[request.sourceTabId] };
					delete next[request.sourceTabId];
					return next;
				});
				setActiveTabId(nextId);
			}
			setFilePathRequest(null);
		} catch (error) {
			setFilePathRequest((current) =>
				current
					? {
							...current,
							submitting: false,
							error: error instanceof Error ? error.message : String(error),
						}
					: current,
			);
		}
	}, [closeDocument, filePathRequest, openCreatedDocument, openFile, saveFile]);

	const openFolder = useCallback(async () => {
		if (workspaceSwitching) return;
		setWorkspaceSwitching(true);
		try {
			const path = await chooseWorkspaceFolder();
			if (!path) {
				setWorkspaceSwitching(false);
				return;
			}

			await replaceWorkspace(path);
			window.location.replace("/");
		} catch (error) {
			setWorkspaceSwitching(false);
			toast({
				title: "Could not open folder",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		}
	}, [toast, workspaceSwitching]);

	useEffect(() => {
		for (const [command, enabled] of [
			["new-file", !workspaceSwitching],
			["open-folder", !workspaceSwitching],
			["save", canSaveFile],
			["save-as", canSaveFile],
		] as const) {
			window.dispatchEvent(
				new CustomEvent("shell:command-state", {
					detail: { command, enabled },
				}),
			);
		}
	}, [canSaveFile, workspaceSwitching]);

	useEffect(() => {
		const onShellCommand = (event: Event) => {
			const command = (event as CustomEvent<unknown>).detail;
			switch (command) {
				case "new-file":
					setFilePathRequest({ kind: "new", path: "", submitting: false });
					break;
				case "open-folder":
					if (dirtyPaths.size > 0) setOpenFolderRequest(true);
					else void openFolder();
					break;
				case "save":
					if (canSaveFile && activeTab.path) {
						void saveFile(activeTab.path);
					}
					break;
				case "save-as":
					if (canSaveFile && activeTab.path && activeDocument) {
						setFilePathRequest({
							kind: "save-as",
							path: activeTab.path,
							sourcePath: activeTab.path,
							sourceTabId: activeTab.id,
							content: activeDocument.draft,
							submitting: false,
						});
					}
					break;
			}
		};
		window.addEventListener("shell:command", onShellCommand);
		return () => window.removeEventListener("shell:command", onShellCommand);
	}, [
		activeDocument,
		activeTab,
		canSaveFile,
		dirtyPaths.size,
		openFolder,
		saveFile,
	]);

	const requestCloseTab = useCallback(
		async (id: string) => {
			const tab = tabs.find((item) => item.id === id);
			if (!tab || !isClosableTab(tab)) return;
			if (tab.type === "debug") {
				try {
					const current = await queryClient.fetchQuery({
						queryKey: queryKeys.debug.session,
						queryFn: ({ signal }) => getDebugSession(signal),
						staleTime: 0,
					});
					if (current.session && current.session.state !== "terminated") {
						const result = await controlDebug(
							"stop",
							current.session.session_id,
						);
						applyDebugSession(result.session);
					}
					setDebugContentView("output");
					closeTabNow(tab.id);
				} catch (error) {
					toast({
						title: "Could not stop debugger",
						description: error instanceof Error ? error.message : String(error),
						tone: "error",
					});
				}
				return;
			}
			if (tab.type === "file" && tab.path && dirtyPaths.has(tab.path)) {
				setCloseRequest({ kind: "file", tab });
				return;
			}
			if (tab.type === "terminal" && tab.terminalId) {
				try {
					for (let attempt = 0; attempt < 2; attempt++) {
						const terminal = await getTerminal(tab.terminalId);
						if (!terminal) {
							closeTabNow(tab.id);
							return;
						}
						if (!terminal.busy) {
							await closeTerminal(tab);
							return;
						}
						if (attempt === 0) {
							await new Promise((resolve) => window.setTimeout(resolve, 100));
						}
					}
					setCloseRequest({ kind: "terminal", tab });
				} catch (error) {
					toast({
						title: "Could not check terminal",
						description: error instanceof Error ? error.message : String(error),
						tone: "error",
					});
				}
				return;
			}
			closeTabNow(id);
		},
		[
			applyDebugSession,
			closeTabNow,
			closeTerminal,
			dirtyPaths,
			queryClient,
			tabs,
			toast,
		],
	);

	const [tabMenu, setTabMenu] = useState<{
		x: number;
		y: number;
		tabId?: string;
	} | null>(null);

	const openTabMenu = useCallback(
		(x: number, y: number, tabId: string | undefined) => {
			const tab = tabs.find((item) => item.id === tabId);
			setTabMenu({
				x,
				y,
				tabId: tab && isClosableTab(tab) ? tab.id : undefined,
			});
		},
		[tabs],
	);

	const moveTabToPane = useCallback(
		(tabId: string, side: PaneSide, index?: number) => {
			const tab = tabs.find((item) => item.id === tabId);
			if (!tab || paneOf(tab) === side) return;
			if (side === "right" && leftTabs.length < 2) return;
			setTabs((prev) =>
				moveTab(
					prev.map((item) =>
						item.id === tabId
							? {
									...item,
									pane: side === "right" ? "right" : undefined,
									preview: undefined,
								}
							: item,
					),
					tabId,
					index ?? prev.length,
				),
			);
			activateTab(tab);
		},
		[activateTab, leftTabs, tabs],
	);

	// Each strip shows only its pane's tabs; drop positions are translated
	// back to tabs-array positions, and cross-strip drops switch the pane.
	const handleStripDrop = useCallback(
		(side: PaneSide, stripIndex: number) => {
			if (!dragTabId) return;
			setDragTabId(null);
			const dragged = tabs.find((tab) => tab.id === dragTabId);
			if (!dragged) return;
			const group = tabs.filter((tab) => paneOf(tab) === side);
			const position = Math.min(stripIndex, group.length);
			const index =
				position < group.length
					? tabs.findIndex((tab) => tab.id === group[position].id)
					: tabs.length;
			if (paneOf(dragged) === side) {
				setTabs((prev) => moveTab(prev, dragTabId, index));
			} else {
				moveTabToPane(dragTabId, side, index);
			}
		},
		[dragTabId, moveTabToPane, tabs],
	);

	const handleZoneDrop = useCallback(
		(zone: PaneZone) => {
			if (!dragTabId) return;
			moveTabToPane(dragTabId, zone);
			setDragTabId(null);
		},
		[dragTabId, moveTabToPane],
	);

	const closeTabs = useCallback(
		async (ids: string[]) => {
			for (const id of ids) {
				await requestCloseTab(id);
			}
		},
		[requestCloseTab],
	);

	useEffect(() => {
		const onKey = (event: KeyboardEvent) => {
			if (event.metaKey && !event.ctrlKey && event.key.toLowerCase() === "w") {
				event.preventDefault();
				void requestCloseTab(activeTabId);
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [requestCloseTab, activeTabId]);

	// Monaco leaves Ctrl/Cmd+S unbound, so saves from any focus location land
	// here; preventDefault also suppresses the browser's save dialog.
	useEffect(() => {
		const onKey = (event: KeyboardEvent) => {
			if (
				!(event.metaKey || event.ctrlKey) ||
				event.shiftKey ||
				event.altKey ||
				event.key.toLowerCase() !== "s"
			) {
				return;
			}
			event.preventDefault();
			if (canSaveFile && activeTab.path) void saveFile(activeTab.path);
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [canSaveFile, activeTab.path, saveFile]);

	const terminateTerminal = useCallback(async () => {
		const request = closeRequest;
		if (request?.kind !== "terminal" || !request.tab.terminalId) return;
		await closeTerminal(request.tab);
	}, [closeRequest, closeTerminal]);

	const saveAndCloseFile = useCallback(async () => {
		const request = closeRequest;
		if (request?.kind !== "file" || !request.tab.path) return;
		await saveFile(request.tab.path, request.tab.id);
	}, [closeRequest, saveFile]);

	const overwriteChangedFile = useCallback(async () => {
		const request = saveConflict;
		if (!request) return;
		const result = await saveDocument(request.path, true);
		if (!result.ok) {
			toast({
				title: "Could not save file",
				description: result.error,
				tone: "error",
			});
			return;
		}
		setSaveConflict(null);
		if (request.closeTabId) closeTabNow(request.closeTabId);
	}, [closeTabNow, saveConflict, saveDocument, toast]);

	const setTerminalTitle = useCallback((id: string, title: string) => {
		if (!title) return;
		setTabs((prev) =>
			prev.map((tab) =>
				tab.type === "terminal" && tab.terminalId === id
					? { ...tab, label: title }
					: tab,
			),
		);
	}, []);

	useEffect(() => {
		if (tabs.some((t) => t.id === activeTabId)) return;
		activateTab(tabs[0] ?? draftChatTab());
	}, [tabs, activeTabId, activateTab]);

	useEffect(() => {
		activePaneRef.current = activeTab.pane;
		if (activeTab.pane === "right") setRightActiveId(activeTab.id);
		else setLeftActiveId(activeTab.id);
	}, [activeTab.id, activeTab.pane]);

	useEffect(() => {
		if (tabs.length === 0) return;
		if (tabs.some((tab) => paneOf(tab) === "left")) return;
		setTabs((prev) =>
			prev.map((tab) => (tab.pane ? { ...tab, pane: undefined } : tab)),
		);
	}, [tabs]);

	useEffect(() => {
		if (!currentSessionId) return;
		if (
			tabs.some(
				(tab) => tab.type === "chat" && tab.sessionId === currentSessionId,
			)
		) {
			return;
		}
		const fallback = tabs.find((tab) => tab.type === "chat");
		setCurrentSessionId(fallback?.sessionId ?? "");
	}, [currentSessionId, tabs]);

	const handleNewSession = useCallback(async () => {
		try {
			const id = await createSessionMutation();
			openChatTab(id, "keep", true);
		} catch (error) {
			toast({
				title: "Could not create session",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		}
	}, [createSessionMutation, openChatTab, toast]);

	const handleSessionDeleted = useCallback(
		(id: string) => {
			removeSession(id);
			setCurrentSessionId((prev) => (prev === id ? "" : prev));
			const tab = tabs.find((t) => t.type === "chat" && t.sessionId === id);
			if (tab) closeTabNow(tab.id);
		},
		[removeSession, tabs, closeTabNow],
	);

	const confirmSessionDelete = useCallback(async () => {
		if (!sessionDelete) return;
		try {
			await deleteSession(sessionDelete.id);
			const id = sessionDelete.id;
			queryClient.setQueryData(
				queryKeys.sessions.list,
				(current: SessionInfo[] = []) =>
					current.filter((session) => session.id !== id),
			);
			setSessionDelete(null);
			handleSessionDeleted(id);
		} catch (error) {
			toast({
				title: "Session was not deleted",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		}
	}, [handleSessionDeleted, queryClient, sessionDelete, toast]);

	const [sessionLoad, setSessionLoad] = useState<{
		id: string;
		loading: boolean;
		error: string | null;
	}>({ id: "", loading: false, error: null });
	const loadReqRef = useRef(0);
	const sessionLoadRequestsRef = useRef(
		new Map<string, Promise<string | null>>(),
	);

	const handleSessionSelect = useCallback(
		async (id: string, disposition: TabDisposition = "keep") => {
			const req = ++loadReqRef.current;
			openChatTab(id, disposition);
			if (hasSession(id)) {
				setSessionLoad({ id: "", loading: false, error: null });
				return;
			}
			setSessionLoad({ id, loading: true, error: null });
			let request = sessionLoadRequestsRef.current.get(id);
			if (!request) {
				request = (async () => {
					try {
						await loadSession(id);
						return null;
					} catch (error) {
						return error instanceof Error
							? error.message
							: "Failed to load session.";
					}
				})();
				sessionLoadRequestsRef.current.set(id, request);
				void request.finally(() => {
					if (sessionLoadRequestsRef.current.get(id) === request) {
						sessionLoadRequestsRef.current.delete(id);
					}
				});
			}
			const error = await request;
			if (loadReqRef.current !== req) return;
			setSessionLoad({ id, loading: false, error });
		},
		[openChatTab, hasSession],
	);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type !== "agent_changed") return;
			clearSessions();
			setTabs((prev) => [
				draftChatTab(),
				...prev.filter((t) => t.type !== "chat"),
			]);
			setActiveTabId(chatTabId(""));
			setCurrentSessionId("");
			const target = deepLinkRef.current;
			deepLinkRef.current = null;
			if (target) {
				void handleSessionSelect(target);
			} else {
				void handleNewSession();
			}
		});
	}, [subscribe, clearSessions, handleSessionSelect, handleNewSession]);

	useEffect(() => {
		const [agent, sid] = window.location.pathname
			.split("/")
			.filter(Boolean)
			.map(decodeURIComponent);
		(async () => {
			deepLinkRef.current = sid ?? null;
			try {
				const currentState = await queryClient.fetchQuery(
					agentQueries.current(),
				);
				const current = currentState.agent || BUILTIN_AGENT_ID;
				if (!agent || agent === current) {
					if (sid) void handleSessionSelect(sid);
					deepLinkRef.current = null;
					return;
				}
				await setCurrentAgent(agent);
				await queryClient.invalidateQueries({
					queryKey: queryKeys.agents.current,
					exact: true,
				});
			} catch {
				deepLinkRef.current = null;
			}
		})();
		// eslint-disable-next-line react-hooks/exhaustive-deps -- initial URL only
	}, [queryClient]);

	const createSessionForDraft = useCallback(async (): Promise<string> => {
		const id = await createSessionMutation();
		openChatTab(id, "keep", true);
		return id;
	}, [createSessionMutation, openChatTab]);

	const sendForSession = useCallback(
		async (
			key: string,
			text: string,
			files?: string[],
			images?: string[],
			intent: TurnInputIntent = "follow_up",
		): Promise<boolean> => {
			try {
				const sid = key || (await createSessionForDraft());
				return sendChat(sid, text, files, images, intent);
			} catch {
				return false;
			}
		},
		[sendChat, createSessionForDraft],
	);

	const handleSend = useCallback(
		(
			text: string,
			files?: string[],
			images?: string[],
			intent: TurnInputIntent = "follow_up",
		): Promise<boolean> =>
			sendForSession(sessionId, text, files, images, intent),
		[sendForSession, sessionId],
	);

	const startInsightsAnalysis = useCallback(
		async (command: string) => {
			try {
				const id = await createSessionMutation();
				openChatTab(id, "keep");
				if (!sendChat(id, command)) {
					throw new Error("The chat connection is not ready");
				}
			} catch (error) {
				toast({
					title: "Could not start analysis",
					description: error instanceof Error ? error.message : String(error),
					tone: "error",
				});
			}
		},
		[createSessionMutation, openChatTab, sendChat, toast],
	);

	const focusChat = useCallback(() => {
		const tab =
			tabs.find((t) => t.type === "chat" && t.sessionId === currentSessionId) ??
			tabs.find((t) => t.type === "chat");
		if (tab) activateTab(tab);
	}, [tabs, currentSessionId, activateTab]);

	const runSkill = useCallback(
		(skill: PaletteSkill) => {
			focusChat();
			if (skill.input_hint) {
				setComposerSeed({ text: `/${skill.name} `, nonce: Date.now() });
				return;
			}
			void handleSend(`/${skill.name}`);
		},
		[focusChat, handleSend],
	);

	const visibleSessionKeys = useMemo(() => {
		const keys = new Set<string>([sessionId]);
		for (const tab of [leftTab, rightTab]) {
			if (tab?.type === "chat") keys.add(tab.sessionId ?? "");
		}
		return [...keys];
	}, [leftTab, rightTab, sessionId]);
	const modeQueries = useQueries({
		queries: visibleSessionKeys.map((key) => sessionQueries.mode(key)),
	});
	const modeStates = useMemo<Record<string, ModeState>>(
		() =>
			Object.fromEntries(
				visibleSessionKeys.flatMap((key, index) => {
					const data = modeQueries[index]?.data;
					return data ? [[key, data]] : [];
				}),
			),
		[modeQueries, visibleSessionKeys],
	);

	const selectModeForSession = useCallback(
		async (key: string, next: string) => {
			const previous = queryClient.getQueryData<ModeState>(
				queryKeys.modes.current(key),
			);
			try {
				const sid = key || (await createSessionForDraft());
				queryClient.setQueryData<ModeState>(queryKeys.modes.current(key), {
					modes: previous?.modes ?? [],
					mode: next,
				});
				const data = await setMode(sid, next);
				queryClient.setQueryData<ModeState>(queryKeys.modes.current(sid), {
					modes: data.modes,
					mode: data.mode,
				});
				if (key !== sid) {
					queryClient.removeQueries({
						queryKey: queryKeys.modes.current(key),
						exact: true,
					});
				}
			} catch (error) {
				queryClient.setQueryData(queryKeys.modes.current(key), previous);
				toast({
					title: "Could not change mode",
					description: error instanceof Error ? error.message : String(error),
					tone: "error",
				});
			}
		},
		[createSessionForDraft, queryClient, toast],
	);

	const modes = modeStates[sessionId]?.modes ?? EMPTY_MODES;
	const mode = modeStates[sessionId]?.mode ?? "";
	const selectMode = useCallback(
		(next: string) => selectModeForSession(sessionId, next),
		[selectModeForSession, sessionId],
	);

	const handleLeftPanelResize = useCallback(({ inPixels }: PanelSize) => {
		setLeftPanelCollapsed(inPixels === 0);
		if (inPixels <= 0) return;
		const width = Math.round(inPixels);
		leftPanelWidthRef.current = width;
		appRef.current?.style.setProperty("--left-panel-width", `${width}px`);
	}, []);
	const handleRightPanelResize = useCallback(({ inPixels }: PanelSize) => {
		setRightPanelCollapsed(inPixels === 0);
		if (inPixels <= 0) return;
		const width = Math.round(inPixels);
		rightPanelWidthRef.current = width;
		appRef.current?.style.setProperty("--right-panel-width", `${width}px`);
	}, []);
	const handleRightPaneResize = useCallback(({ inPixels }: PanelSize) => {
		if (inPixels <= 0) return;
		appRef.current?.style.setProperty(
			"--right-pane-width",
			`${Math.round(inPixels)}px`,
		);
	}, []);
	const handleDebugDetailsResize = useCallback(({ inPixels }: PanelSize) => {
		const visible = inPixels > 0;
		setDebugDetailsVisible(visible);
		if (visible) debugDetailsWidthRef.current = Math.round(inPixels);
	}, []);
	const toggleLeftPanel = useCallback(() => {
		const panel = leftPanelRef.current;
		if (panel?.isCollapsed()) {
			panel.resize(`${leftPanelWidthRef.current}px`);
		} else panel?.collapse();
	}, [leftPanelRef]);
	const toggleRightPanel = useCallback(() => {
		const panel = rightPanelRef.current;
		if (panel?.isCollapsed()) {
			panel.resize(`${rightPanelWidthRef.current}px`);
		} else panel?.collapse();
	}, [rightPanelRef]);
	const showRightPanel = useCallback(
		(tab: WorkspaceTab) => {
			setRequestedWorkspaceTab(tab);
			if (rightPanelRef.current?.isCollapsed()) {
				rightPanelRef.current.resize(`${rightPanelWidthRef.current}px`);
			}
		},
		[rightPanelRef],
	);
	const toggleDebugDetails = useCallback(() => {
		const panel = debugDetailsPanelRef.current;
		if (!panel) {
			setDebugDetailsVisible((visible) => !visible);
			return;
		}
		if (panel.isCollapsed()) {
			setDebugDetailsVisible(true);
			panel.resize(`${DEBUG_DETAILS_MIN_SIZE}px`);
		} else {
			setDebugDetailsVisible(false);
			panel.collapse();
		}
	}, [debugDetailsPanelRef]);
	const showDebugDetails = useCallback(() => {
		setDebugDetailsVisible(true);
		if (debugDetailsPanelRef.current?.isCollapsed()) {
			debugDetailsPanelRef.current.resize(`${DEBUG_DETAILS_MIN_SIZE}px`);
		}
	}, [debugDetailsPanelRef]);
	const showDebugger = useCallback(() => {
		const terminalId =
			debugSession?.state !== "terminated" &&
			debugSession?.terminal_id &&
			!exitedDebugTerminalIDsRef.current.has(debugSession.terminal_id)
				? debugSession.terminal_id
				: undefined;
		setTabs((current) =>
			syncDebugTab(current, terminalId, true, activePaneRef.current),
		);
		if (terminalId) setDebugContentView("terminal");
		showDebugDetails();
		setActiveTabId("debug");
	}, [debugSession, showDebugDetails]);
	const showDebugSession = useCallback(
		(session: DebugSession) => {
			if (session.terminal_id)
				exitedDebugTerminalIDsRef.current.delete(session.terminal_id);
			applyDebugSession(session);
			void queryClient.invalidateQueries({
				queryKey: queryKeys.debug.state,
				exact: true,
			});
			setDebugContentView(session.terminal_id ? "terminal" : "output");
			showDebugDetails();
			setActiveTabId("debug");
		},
		[applyDebugSession, queryClient, showDebugDetails],
	);
	const showDebugFailure = useCallback(
		(session: DebugSession) => {
			applyDebugSession(session);
			setTabs((current) =>
				syncDebugTab(current, undefined, true, activePaneRef.current),
			);
			setDebugContentView("output");
			showDebugDetails();
			setActiveTabId("debug");
		},
		[applyDebugSession, showDebugDetails],
	);
	const handleDebugTerminalExit = useCallback((id: string) => {
		exitedDebugTerminalIDsRef.current.add(id);
		setTabs((current) => syncDebugTab(current, undefined, false));
		setDebugContentView("output");
	}, []);
	const showWorkspaceSearch = useCallback(() => {
		showRightPanel("files");
		setWorkspaceSearching(true);
		setSearchFocusKey((value) => value + 1);
	}, [showRightPanel]);
	useEffect(() => {
		const onKey = (event: KeyboardEvent) => {
			if (
				!(event.metaKey || event.ctrlKey) ||
				!event.shiftKey ||
				event.key.toLowerCase() !== "f"
			)
				return;
			event.preventDefault();
			showWorkspaceSearch();
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [showWorkspaceSearch]);

	const paletteActions = useMemo<PaletteAction[]>(() => {
		const actions: PaletteAction[] = [
			{
				id: "new-session",
				label: "New session",
				icon: <Plus size={12} className="text-fg-dim shrink-0" />,
				run: () => void handleNewSession(),
			},
			{
				id: "toggle-agent-sessions",
				label: leftPanelCollapsed
					? "Show Agent Sessions"
					: "Hide Agent Sessions",
				icon: <PanelLeftOpen size={12} className="text-fg-dim shrink-0" />,
				run: toggleLeftPanel,
			},
			{
				id: "toggle-workspace-panel",
				label: rightPanelCollapsed
					? "Show Workspace Panel"
					: "Hide Workspace Panel",
				icon: <PanelRightOpen size={12} className="text-fg-dim shrink-0" />,
				run: toggleRightPanel,
			},
		];
		if (showChanges) {
			actions.push({
				id: "show-changes",
				label: "Show changes",
				icon: <GitCompare size={12} className="text-fg-dim shrink-0" />,
				run: () => showRightPanel("changes"),
			});
		}
		if (showTerminal) {
			if (terminalShells.length === 0) {
				actions.push({
					id: "new-terminal",
					label: "New Terminal",
					hint: TERMINAL_SHORTCUT,
					icon: <SquareTerminal size={12} className="text-fg-dim shrink-0" />,
					run: () => void createTerminal(),
				});
			} else {
				for (const [index, shell] of terminalShells.entries()) {
					actions.push({
						id: `new-terminal-${index}`,
						label: `New Terminal (${terminalShellName(shell.name)})`,
						hint: index === 0 ? TERMINAL_SHORTCUT : undefined,
						icon: <SquareTerminal size={12} className="text-fg-dim shrink-0" />,
						run: () => void createTerminal(shell.id),
					});
				}
			}
		}
		if (tabAvailable) {
			actions.push({
				id: "editor.tab.completion",
				label: `${tabEnabled ? "Disable" : "Enable"} editor.tab.completion`,
				hint: `${tabEnabled ? "On" : "Off"} · uses model requests while typing`,
				icon: <Sparkles size={12} className="text-fg-dim shrink-0" />,
				run: () => void toggleEditorTabCompletion(),
			});
		}
		actions.push({
			id: "find-in-files",
			label: "Find in files",
			hint: /Mac|iPhone|iPad/.test(navigator.platform) ? "⇧⌘F" : "Ctrl+Shift+F",
			icon: <Search size={12} className="text-fg-dim shrink-0" />,
			run: showWorkspaceSearch,
		});
		actions.push({
			id: "code-graph",
			label: "Open insights",
			icon: <Lightbulb size={12} className="text-fg-dim shrink-0" />,
			run: openInsightsTab,
		});
		actions.push({
			id: "show-files",
			label: "Show files",
			icon: <FileText size={12} className="text-fg-dim shrink-0" />,
			run: () => showRightPanel("files"),
		});
		for (const m of modes) {
			if (m.id === mode) continue;
			const Icon = /plan|read|only/i.test(m.id) ? Compass : Wrench;
			actions.push({
				id: `mode-${m.id}`,
				label: `Switch to ${m.name} mode`,
				hint: m.description,
				icon: <Icon size={12} className="text-fg-dim shrink-0" />,
				run: () => void selectMode(m.id),
			});
		}
		return actions;
	}, [
		handleNewSession,
		showChanges,
		showTerminal,
		tabAvailable,
		tabEnabled,
		toggleEditorTabCompletion,
		leftPanelCollapsed,
		rightPanelCollapsed,
		terminalShells,
		toggleLeftPanel,
		toggleRightPanel,
		showRightPanel,
		showWorkspaceSearch,
		openInsightsTab,
		createTerminal,
		modes,
		mode,
		selectMode,
	]);

	const runningSessionIds = new Set(
		Object.values(sessions)
			.filter((s) => s.phase !== "idle")
			.map((s) => s.id),
	);

	const chatTabLabel = (tab: CenterTab): string => {
		if (!tab.sessionId) return tab.label;
		const sess = sessions[tab.sessionId];
		const firstUser = sess?.entries.find(
			(e) => e.type === "user" && e.content.trim(),
		);
		if (!firstUser) return "Session";
		const text = firstUser.content.trim().replace(/\s+/g, " ");
		return text.length > 24 ? `${text.slice(0, 24)}…` : text;
	};

	const stripItem = (tab: CenterTab): TabStripItem => ({
		tab,
		label: tab.type === "chat" ? chatTabLabel(tab) : tab.label,
		dirty: tab.type === "file" && !!tab.path && dirtyPaths.has(tab.path),
		running:
			tab.type === "chat" && tab.sessionId
				? (sessions[tab.sessionId]?.phase ?? "idle") !== "idle"
				: false,
		closable: isClosableTab(tab),
	});
	const leftStripItems: TabStripItem[] = leftPool.map(stripItem);
	const rightStripItems: TabStripItem[] = rightTabs.map(stripItem);

	const renderTabContent = (tab: CenterTab): ReactNode => {
		if (tab.type === "chat") {
			const key = tab.sessionId ?? "";
			const sess = key ? sessions[key] : undefined;
			const modeState = modeStates[key];
			return (
				<ChatPanel
					key={tab.id}
					sessionId={key}
					entries={sess?.entries ?? EMPTY_ENTRIES}
					phase={sess?.phase ?? "idle"}
					modes={modeState?.modes ?? EMPTY_MODES}
					mode={modeState?.mode ?? ""}
					onSelectMode={(next) => void selectModeForSession(key, next)}
					onSend={(text, files, images, intent) =>
						sendForSession(key, text, files, images, intent)
					}
					onCancel={(clear) => {
						if (key) cancel(key, clear ?? false);
					}}
					pendingInputs={sess?.pendingInputs ?? EMPTY_ENTRIES}
					queuePaused={sess?.queuePaused ?? false}
					canSteer={sess?.canSteer ?? false}
					onRemoveQueued={(id, state) => {
						if (!key) return;
						if (state === "queued" || state === "sending") {
							removeQueued(key, id);
						} else {
							dismissPending(key, id);
						}
					}}
					onUpdateQueued={(id, text, files, images) =>
						key ? updateQueued(key, id, text, files, images) : false
					}
					onResumeQueue={() => {
						if (key) resumeQueue(key);
					}}
					onClearQueue={() => {
						if (key) clearQueue(key);
					}}
					loading={sessionLoad.loading && sessionLoad.id === key}
					loadError={sessionLoad.id === key ? sessionLoad.error : null}
					error={sess?.error ?? null}
					onDismissError={() => {
						if (key) dismissError(key);
					}}
					prompt={sess?.prompt ?? null}
					onPromptReply={(reply) => {
						const pending = sess?.prompt;
						if (key && pending) respondPrompt(key, pending.id, reply);
					}}
					seed={tab.id === activeTabId ? composerSeed : null}
					toolProgress={toolProgress}
				/>
			);
		}
		if (tab.type === "debug") {
			return (
				<Group
					id="debug-layout"
					orientation="horizontal"
					className="h-full min-h-0 min-w-0 overflow-hidden"
				>
					<Panel
						id="debug-content"
						minSize="160px"
						className="relative min-h-0 min-w-0 overflow-hidden"
					>
						<div
							className={debugContentView === "output" ? "h-full" : "hidden"}
						>
							<DebugOutputTab />
						</div>
						{tab.terminalId && (
							<div
								className={
									debugContentView === "terminal" ? "h-full" : "hidden"
								}
							>
								<TerminalView
									id={tab.terminalId}
									active={debugContentView === "terminal"}
									onExit={handleDebugTerminalExit}
									onTitle={setTerminalTitle}
								/>
							</div>
						)}
					</Panel>
					<ResizeHandle
						label="Resize debugger details"
						hidden={!debugDetailsVisible}
					/>
					<Panel
						id="debug-details"
						panelRef={debugDetailsPanelRef}
						defaultSize={
							debugDetailsVisible ? `${debugDetailsWidthRef.current}px` : "0px"
						}
						minSize={`${DEBUG_DETAILS_MIN_SIZE}px`}
						maxSize={`${DEBUG_DETAILS_MAX_SIZE}px`}
						collapsedSize="0px"
						collapsible
						groupResizeBehavior="preserve-pixel-size"
						onResize={handleDebugDetailsResize}
						inert={!debugDetailsVisible}
						className="h-full overflow-hidden border-l border-border-subtle"
					>
						<div className="h-full" aria-label="Debugger details">
							<DebugTab
								onOpenFile={(path, line, column) =>
									openFile(path, line, column)
								}
								onStopped={showDebugDetails}
							/>
						</div>
					</Panel>
				</Group>
			);
		}
		if (tab.type === "terminal" && tab.terminalId) {
			return (
				<TerminalView
					key={tab.terminalId}
					id={tab.terminalId}
					active
					onExit={() => closeTabNow(tab.id)}
					onTitle={setTerminalTitle}
				/>
			);
		}
		if (tab.type === "task" && tab.taskId) {
			return (
				<TaskTab
					key={tab.id}
					sessionId={tab.sessionId ?? ""}
					taskId={tab.taskId}
				/>
			);
		}
		if (tab.type === "graph") {
			return (
				<InsightsTab
					key={tab.id}
					onStartAnalysis={(command) => void startInsightsAnalysis(command)}
					onOpenFile={(path, line, column) => openFile(path, line, column)}
				/>
			);
		}
		if (
			tab.type === "compare" &&
			tab.compareBase &&
			tab.compareHead &&
			tab.compareMode
		) {
			return (
				<CompareTab
					key={tab.id}
					base={tab.compareBase}
					head={tab.compareHead}
					mode={tab.compareMode}
				/>
			);
		}
		if (tab.type === "diff" && tab.path) {
			return (
				<DiffTab
					path={tab.path}
					layer={tab.diffLayer}
					sessionId={sessionId}
					onDeleted={() => closeTabNow(tab.id)}
				/>
			);
		}
		if (tab.path && documents[tab.path]) {
			return (
				<FileTab
					key={`${tab.id}:${tab.path}`}
					document={documents[tab.path]}
					tabEnabled={tabEnabled}
					line={tab.line}
					column={tab.column}
					navigationKey={tab.navigationKey}
					subscribe={subscribe}
					onChange={(value) => {
						keepTab(tab.id);
						updateDraft(tab.path!, value);
					}}
					onSave={async () => {
						return saveFile(tab.path!);
					}}
					onReload={() => void reloadDocument(tab.path!, tab.external ?? false)}
					onOpenFile={openFile}
					onApplyWorkspaceEdit={requestWorkspaceEdit}
					onLaunchDebug={(target, action) => {
						const currentPath = tab.path;
						if (!currentPath) return;
						if (debugSession && debugSession.state !== "terminated") {
							showDebugger();
							toast({
								title: "Debug session already active",
								description: "Stop it before starting another target.",
							});
							return;
						}
						openDebugLauncher({
							target,
							action,
							currentPath,
						});
					}}
					view={fileViews[tab.id] ?? defaultFileView(tab.path)}
				/>
			);
		}
		return null;
	};

	const renderPane = (tab: CenterTab | undefined): ReactNode => (
		<div
			className="relative h-full min-h-0 min-w-0 overflow-hidden bg-bg"
			onPointerDownCapture={() => {
				if (!tab) return;
				if (tab.id !== activeTabId) activateTab(tab);
				if (tab.preview) keepTab(tab.id);
			}}
			onKeyDownCapture={() => {
				if (tab?.preview) keepTab(tab.id);
			}}
		>
			<ErrorBoundary
				key={tab?.id ?? "empty"}
				fallback={(error, _reset, errorInfo) => (
					<TabCrashed error={error} errorInfo={errorInfo} />
				)}
			>
				{tab ? renderTabContent(tab) : null}
			</ErrorBoundary>
		</div>
	);

	const dragTab = dragTabId
		? tabs.find((tab) => tab.id === dragTabId)
		: undefined;
	const renderCenterContent = (): ReactNode => (
		<div className="relative h-full min-h-0 min-w-0 overflow-hidden">
			{rightTab ? (
				<Group
					id="pane-split"
					orientation="horizontal"
					className="h-full min-h-0 min-w-0 overflow-hidden"
				>
					<Panel
						id="pane-left"
						minSize="160px"
						className="min-h-0 min-w-0 overflow-hidden"
					>
						{renderPane(leftTab)}
					</Panel>
					<ResizeHandle label="Resize right pane" hidden={false} />
					<Panel
						id="pane-right"
						minSize="160px"
						defaultSize="35%"
						onResize={handleRightPaneResize}
						className="min-h-0 min-w-0 overflow-hidden border-l border-border-subtle"
					>
						{renderPane(rightTab)}
					</Panel>
				</Group>
			) : (
				renderPane(leftTab)
			)}
			{dragTab && (
				<PaneDropZones
					allowLeft={paneOf(dragTab) === "right"}
					allowRight={paneOf(dragTab) === "left" && leftTabs.length > 1}
					onDrop={handleZoneDrop}
				/>
			)}
		</div>
	);

	const canCreateNew = !!(
		sessionId && (sessions[sessionId]?.entries.length ?? 0) > 0
	);
	const leftPanelDocked = !leftPanelCollapsed;
	const rightPanelDocked = !rightPanelCollapsed;
	const agentSessionsContent = (
		<AgentSessions
			currentSessionId={sessionId}
			onSessionSelect={(id, disposition) => {
				void handleSessionSelect(id, disposition);
			}}
			onSessionDelete={(id, title) => setSessionDelete({ id, title })}
			runningSessionIds={runningSessionIds}
			switchingAgent={switchingAgent}
		/>
	);
	const titlebarActions = (
		<div
			data-window-interactive
			data-titlebar-actions
			className="flex shrink-0 items-center pr-2"
		>
			{activeTab.type === "chat" &&
				(usage.inputTokens > 0 || outputTokens > 0) && (
					<UsageIndicator
						inputTokens={usage.inputTokens}
						cachedTokens={usage.cachedTokens}
						outputTokens={outputTokens}
						lastInputTokens={usage.lastInputTokens}
						contextWindow={usage.contextWindow}
						outputEstimated={streamEstimate > 0}
					/>
				)}
			{canCreateNew && (
				<button
					type="button"
					className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
					onClick={handleNewSession}
					title="New session"
					aria-label="New session"
				>
					<Plus size={13} />
				</button>
			)}
			{activePreviewKind && (
				<button
					type="button"
					className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors hover:bg-bg-hover ${
						activeFileView === "preview"
							? "text-fg-muted"
							: "text-fg-dim hover:text-fg-muted"
					}`}
					onClick={() => {
						keepTab(activeTab.id);
						setFileViews((prev) => ({
							...prev,
							[activeTab.id]: activeFileView === "preview" ? "code" : "preview",
						}));
					}}
					title={previewToggleLabel}
					aria-label={previewToggleLabel}
				>
					{activeFileView === "preview" ? (
						<Code2 size={13} />
					) : activePreviewKind === "html" ? (
						<Globe2 size={13} />
					) : (
						<Eye size={13} />
					)}
				</button>
			)}
			{activeTab.type === "debug" && (
				<>
					<button
						type="button"
						className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors hover:bg-bg-hover hover:text-fg-muted ${debugDetailsVisible ? "text-fg-muted" : "text-fg-dim"}`}
						onClick={toggleDebugDetails}
						title={
							debugDetailsVisible
								? "Hide debugger details"
								: "Show debugger details"
						}
						aria-label={
							debugDetailsVisible
								? "Hide debugger details"
								: "Show debugger details"
						}
					>
						<Bug size={13} />
					</button>
					{activeTab.terminalId && (
						<button
							type="button"
							className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
							onClick={() =>
								setDebugContentView((view) =>
									view === "terminal" ? "output" : "terminal",
								)
							}
							title={
								debugContentView === "terminal"
									? "Show debug output"
									: "Show terminal"
							}
							aria-label={
								debugContentView === "terminal"
									? "Show debug output"
									: "Show terminal"
							}
						>
							{debugContentView === "terminal" ? (
								<MonitorPlay size={13} />
							) : (
								<Monitor size={13} />
							)}
						</button>
					)}
				</>
			)}
		</div>
	);

	const workspaceTabs = (
		<div
			className="flex h-10 w-full min-w-0 shrink-0 items-stretch overflow-hidden"
			role="tablist"
			aria-label="Workspace panels"
		>
			<WorkspaceTabButton
				active={workspaceTab === "files"}
				onClick={() => setRequestedWorkspaceTab("files")}
			>
				Files
			</WorkspaceTabButton>
			{showChanges && (
				<WorkspaceTabButton
					active={workspaceTab === "changes"}
					onClick={() => setRequestedWorkspaceTab("changes")}
				>
					Changes
				</WorkspaceTabButton>
			)}
			{showInspect && (
				<WorkspaceTabButton
					active={workspaceTab === "inspect"}
					onClick={() => setRequestedWorkspaceTab("inspect")}
				>
					Inspect
				</WorkspaceTabButton>
			)}
			{showAgents && (
				<WorkspaceTabButton
					active={workspaceTab === "agents"}
					onClick={() => setRequestedWorkspaceTab("agents")}
				>
					Agents
				</WorkspaceTabButton>
			)}
			<div className="flex-1" />
		</div>
	);
	const inspectorContent = (
		<aside
			className="flex h-full flex-col bg-transparent"
			aria-label="Workspace"
		>
			<div
				className="relative min-h-0 flex-1 overflow-hidden pt-0.5"
				role="tabpanel"
			>
				<div
					className={
						workspaceTab === "inspect" ? "flex h-full flex-col" : "hidden"
					}
				>
					<div className="flex h-9 shrink-0 items-center border-b border-border-subtle bg-bg-surface/20 px-3">
						<span className="text-[10px] font-medium uppercase tracking-wide text-fg-dim">
							Diagnostics
						</span>
						<div className="flex-1" />
						<button
							type="button"
							onClick={() => setProblemsRefreshKey((value) => value + 1)}
							title="Refresh diagnostics"
							aria-label="Refresh diagnostics"
							className="flex h-6 w-6 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
						>
							<RefreshCw size={11} />
						</button>
					</div>
					<div className="relative min-h-0 flex-1 overflow-hidden">
						<ProblemsPanel
							onOpenFile={openFile}
							refreshKey={problemsRefreshKey}
						/>
					</div>
				</div>
				{workspaceTab === "inspect" ? null : workspaceTab === "agents" &&
				  showAgents ? (
					<TasksPanel sessionId={sessionId} onOpenTask={openTask} />
				) : workspaceTab === "changes" && showChanges ? (
					<DiffsPanel
						sessionId={sessionId}
						git={capabilities?.git ?? false}
						canInit={capabilities?.git_init ?? false}
						onOpenDiff={openDiff}
						onOpenCompare={openCompare}
						onOpenFile={(path, disposition) =>
							openFile(path, undefined, undefined, undefined, disposition)
						}
					/>
				) : (
					<WorkspaceFilesPanel
						workspaceName={capabilities?.workspace_name ?? "Files"}
						headerContent={
							debugSession && debugSession.state !== "terminated" ? (
								<DebugToolbar
									session={debugSession}
									busy={debugControlBusy}
									onControl={(operation) => void handleDebugControl(operation)}
								/>
							) : undefined
						}
						searching={workspaceSearching}
						searchFocusKey={searchFocusKey}
						onSearch={showWorkspaceSearch}
						onCloseSearch={() => setWorkspaceSearching(false)}
						onOpenInsights={openInsightsTab}
						onFileSelect={(path, disposition) =>
							openFile(path, undefined, undefined, undefined, disposition)
						}
						onFileMove={handleFileMove}
						onOpenSearchResult={(path, line, column, disposition) =>
							openFile(path, line, column, undefined, disposition)
						}
						onApplyWorkspaceEdit={requestWorkspaceEdit}
						platform={capabilities?.platform}
						subscribe={subscribe}
					/>
				)}
			</div>
			<ManagedToolsFooter
				key={capabilities?.managed_tools?.state ?? "none"}
				status={capabilities?.managed_tools}
			/>
		</aside>
	);

	return (
		<div
			ref={appRef}
			className="relative flex h-screen flex-col bg-bg text-fg"
			style={
				{
					"--left-panel-width": `${LEFT_PANEL_DEFAULT_SIZE}px`,
					"--right-panel-width": `${rightPanelDefaultWidth}px`,
					"--right-pane-width": "0px",
				} as CSSProperties
			}
		>
			<div
				data-panel-frame="sessions"
				aria-hidden="true"
				className={`pointer-events-none absolute inset-y-0 left-0 z-0 rounded-[10px] bg-bg-surface/40 transition-[transform,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)] ${
					leftPanelCollapsed
						? "-translate-x-full opacity-0"
						: "translate-x-0 opacity-100"
				}`}
				style={{ width: "var(--left-panel-width)" }}
			/>
			<div
				data-panel-frame="workspace"
				aria-hidden="true"
				className={`pointer-events-none absolute inset-y-0 right-0 z-0 rounded-[10px] bg-bg-surface/40 transition-[transform,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)] ${
					rightPanelCollapsed
						? "translate-x-full opacity-0"
						: "translate-x-0 opacity-100"
				}`}
				style={{ width: "var(--right-panel-width)" }}
			/>
			<header
				data-window-titlebar
				className="window-titlebar relative z-10 flex h-10 shrink-0 items-stretch overflow-hidden bg-transparent"
				aria-label="Window toolbar"
			>
				<div
					data-titlebar-separator
					aria-hidden="true"
					className="pointer-events-none absolute bottom-0 h-px bg-border-subtle"
					style={{
						left: leftPanelDocked ? "var(--left-panel-width)" : "0px",
						right: rightPanelDocked ? "var(--right-panel-width)" : "0px",
					}}
				/>
				<div
					className="window-titlebar-controls-spacer shrink-0"
					aria-hidden="true"
				/>
				<div
					data-window-interactive
					data-titlebar-left-panel
					className="flex shrink-0 items-center gap-0.5 overflow-hidden pr-0 pl-2"
					style={{
						width: leftPanelDocked
							? "calc(var(--left-panel-width) - var(--window-controls-inset))"
							: "40px",
					}}
				>
					{leftPanelDocked && (
						<div data-titlebar-agent className="min-w-0 flex-1 overflow-hidden">
							<AgentPicker onSwitchingChange={setSwitchingAgent} />
						</div>
					)}
					{!leftPanelDocked && <div className="min-w-0 flex-1" />}
					{leftPanelCollapsed && (
						<button
							type="button"
							className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
							onClick={toggleLeftPanel}
							title="Show Agent Sessions"
							aria-label="Show Agent Sessions"
						>
							<PanelLeftOpen size={13} />
						</button>
					)}
				</div>

				<TabStrip
					items={leftStripItems}
					activeTabId={leftTab.id}
					dragTabId={dragTabId}
					onActivate={activateTab}
					onClose={(tab) => void requestCloseTab(tab.id)}
					onKeepOpen={keepTab}
					onContextMenu={openTabMenu}
					onDragStart={setDragTabId}
					onDragEnd={() => setDragTabId(null)}
					onDropTab={(index) => handleStripDrop("left", index)}
				/>
				{tabMenu && (
					<TabContextMenu
						x={tabMenu.x}
						y={tabMenu.y}
						tabId={tabMenu.tabId}
						tabCount={tabs.length}
						preview={!!tabs.find((tab) => tab.id === tabMenu.tabId)?.preview}
						pane={paneOf(
							tabs.find((tab) => tab.id === tabMenu.tabId) ?? leftTab,
						)}
						canMoveRight={leftTabs.length > 1}
						onMove={moveTabToPane}
						onClose={() => setTabMenu(null)}
						onKeepOpen={keepTab}
						onCloseTab={(id) => void closeTabs([id])}
						onCloseOthers={(id) =>
							void closeTabs(
								tabs.filter((tab) => tab.id !== id).map((tab) => tab.id),
							)
						}
						onCloseAll={() => void closeTabs(tabs.map((tab) => tab.id))}
					/>
				)}

				{activeTab.pane !== "right" && titlebarActions}
				{rightTab && (
					<div
						className="flex shrink-0 items-stretch overflow-hidden"
						style={{
							width: `max(0px, calc(var(--right-pane-width) - ${
								rightPanelDocked ? 0 : showTerminal ? 80 : 40
							}px))`,
						}}
					>
						<TabStrip
							items={rightStripItems}
							activeTabId={rightTab.id}
							dragTabId={dragTabId}
							ariaLabel="Right pane tabs"
							onActivate={activateTab}
							onClose={(tab) => void requestCloseTab(tab.id)}
							onKeepOpen={keepTab}
							onContextMenu={openTabMenu}
							onDragStart={setDragTabId}
							onDragEnd={() => setDragTabId(null)}
							onDropTab={(index) => handleStripDrop("right", index)}
						/>
						{activeTab.pane === "right" && titlebarActions}
					</div>
				)}
				<div
					data-window-interactive
					data-titlebar-right-panel
					className="flex shrink-0 items-center overflow-hidden pr-2 pl-0"
					style={{
						width: rightPanelDocked
							? "var(--right-panel-width)"
							: showTerminal
								? "80px"
								: "40px",
					}}
				>
					{rightPanelCollapsed && (
						<button
							type="button"
							className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
							onClick={toggleRightPanel}
							title="Show Workspace Panel"
							aria-label="Show Workspace Panel"
						>
							<PanelRightOpen size={13} />
						</button>
					)}
					{rightPanelDocked && (
						<div
							data-titlebar-workspace-tabs
							className="min-w-0 flex-1 self-stretch"
						>
							{workspaceTabs}
						</div>
					)}
					{!rightPanelDocked && <div className="min-w-0 flex-1" />}
					{showTerminal && (
						<TerminalLauncher
							shells={terminalShells}
							onCreate={(shell) => void createTerminal(shell)}
						/>
					)}
				</div>
			</header>
			<Group
				id="wingman-layout"
				orientation="horizontal"
				className="relative z-10 flex-1 overflow-hidden"
			>
				<Panel
					id="sessions"
					panelRef={leftPanelRef}
					defaultSize="0px"
					minSize={`${LEFT_PANEL_MIN_SIZE}px`}
					maxSize={`${LEFT_PANEL_MAX_SIZE}px`}
					collapsedSize="0px"
					collapsible
					groupResizeBehavior="preserve-pixel-size"
					onResize={handleLeftPanelResize}
					data-layout-panel="sessions"
					inert={leftPanelCollapsed}
					className="h-full overflow-hidden"
				>
					<div
						data-panel-content="sessions"
						className={`h-full overflow-hidden transition-[transform,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)] ${
							leftPanelCollapsed
								? "pointer-events-none -translate-x-full opacity-0"
								: "translate-x-0 opacity-100"
						}`}
						style={{ width: "var(--left-panel-width)" }}
					>
						{agentSessionsContent}
					</div>
				</Panel>
				<ResizeHandle
					label="Resize Agent Sessions"
					hidden={leftPanelCollapsed}
				/>
				<Panel
					id="center"
					minSize={`${CENTER_PANEL_MIN_SIZE}px`}
					data-layout-panel="center"
					className="flex min-w-0 flex-col overflow-hidden bg-bg"
				>
					<main className="flex flex-1 flex-col overflow-hidden min-h-0 bg-bg">
						<div className="min-h-0 flex-1 overflow-hidden">
							{renderCenterContent()}
						</div>
					</main>
				</Panel>
				<ResizeHandle
					label="Resize Workspace Panel"
					hidden={rightPanelCollapsed}
				/>
				<Panel
					id="workspace"
					panelRef={rightPanelRef}
					defaultSize={`${rightPanelDefaultWidth}px`}
					minSize={`${RIGHT_PANEL_MIN_SIZE}px`}
					maxSize={`${RIGHT_PANEL_MAX_SIZE}px`}
					collapsedSize="0px"
					collapsible
					groupResizeBehavior="preserve-pixel-size"
					onResize={handleRightPanelResize}
					data-layout-panel="workspace"
					inert={rightPanelCollapsed}
					className="h-full overflow-hidden"
				>
					<div
						data-panel-content="workspace"
						className={`ml-auto h-full overflow-hidden transition-[transform,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)] ${
							rightPanelCollapsed
								? "pointer-events-none translate-x-full opacity-0"
								: "translate-x-0 opacity-100"
						}`}
						style={{ width: "var(--right-panel-width)" }}
					>
						{inspectorContent}
					</div>
				</Panel>
			</Group>

			{paletteOpen && (
				<CommandPalette
					sessionId={sessionId}
					onClose={() => setPaletteOpen(false)}
					actions={paletteActions}
					onRunSkill={runSkill}
					onSelectSession={(id) => void handleSessionSelect(id, "keep")}
					onOpenFile={openFile}
				/>
			)}

			<DebugLauncher
				open={debugLauncher !== null}
				seed={debugLauncher ?? undefined}
				onClose={() => setDebugLauncher(null)}
				onStarted={(session) => {
					showDebugSession(session);
					toast({
						title:
							session.state === "stopped"
								? "Debugger stopped at target"
								: "Debug session started",
						tone: "success",
					});
				}}
				onFailed={(message) => {
					const previousID = debugSessionRef.current?.session_id;
					void getDebugSession()
						.then(({ session }) => {
							// Only a session created by this launch attempt carries its
							// failure output; a leftover session would hide the error.
							if (session && session.session_id !== previousID) {
								showDebugFailure(session);
								return;
							}
							toast({
								title: "Debugger could not start",
								description: message,
								tone: "error",
							});
						})
						.catch(() => {
							toast({
								title: "Debugger could not start",
								description: message,
								tone: "error",
							});
						});
				}}
			/>

			<Dialog
				open={filePathRequest !== null}
				title={filePathRequest?.kind === "save-as" ? "Save As" : "New File"}
				description="Enter a path relative to the current workspace."
				onClose={() => {
					if (!filePathRequest?.submitting) setFilePathRequest(null);
				}}
				initialFocus="first"
			>
				<form
					className="contents"
					onSubmit={(event) => {
						event.preventDefault();
						void submitFilePath();
					}}
				>
					<label className="flex w-full flex-col gap-1.5 text-[11px] text-fg-muted">
						<span>File path</span>
						<input
							value={filePathRequest?.path ?? ""}
							onChange={(event) =>
								setFilePathRequest((current) =>
									current
										? {
												...current,
												path: event.target.value,
												error: undefined,
											}
										: current,
								)
							}
							onFocus={(event) => {
								if (filePathRequest?.kind === "save-as") {
									event.currentTarget.select();
								}
							}}
							disabled={filePathRequest?.submitting}
							autoComplete="off"
							spellCheck={false}
							className="h-9 rounded-md border border-border-strong bg-bg px-2.5 text-[12px] text-fg outline-none focus:border-focus"
						/>
					</label>
					{filePathRequest?.error && (
						<div className="w-full text-[11px] text-danger" role="alert">
							{filePathRequest.error}
						</div>
					)}
					<button
						type="button"
						className={dialogButtonClass}
						disabled={filePathRequest?.submitting}
						onClick={() => setFilePathRequest(null)}
					>
						Cancel
					</button>
					<button
						type="submit"
						className={dialogPrimaryButtonClass}
						disabled={filePathRequest?.submitting}
					>
						{filePathRequest?.submitting
							? "Saving..."
							: filePathRequest?.kind === "save-as"
								? "Save"
								: "Create"}
					</button>
				</form>
			</Dialog>

			<Dialog
				open={openFolderRequest}
				title="Open another folder?"
				description={`${dirtyPaths.size} unsaved ${dirtyPaths.size === 1 ? "file" : "files"} will be discarded.`}
				onClose={() => setOpenFolderRequest(false)}
			>
				<button
					type="button"
					className={dialogButtonClass}
					onClick={() => setOpenFolderRequest(false)}
				>
					Cancel
				</button>
				<button
					type="button"
					className={`${dialogButtonClass} border-danger/40 text-danger hover:bg-danger/10`}
					onClick={() => {
						setOpenFolderRequest(false);
						void openFolder();
					}}
				>
					Discard and Open
				</button>
			</Dialog>

			<Dialog
				open={closeRequest?.kind === "file"}
				title="Save changes before closing?"
				description={
					closeRequest?.kind === "file"
						? `“${closeRequest.tab.label}” has unsaved changes.`
						: undefined
				}
				onClose={() => setCloseRequest(null)}
			>
				<button
					type="button"
					className={dialogButtonClass}
					onClick={() => setCloseRequest(null)}
				>
					Cancel
				</button>
				<button
					type="button"
					className={`${dialogButtonClass} text-danger`}
					onClick={() => {
						if (closeRequest?.kind !== "file" || !closeRequest.tab.path) return;
						discardDocument(closeRequest.tab.path);
						const id = closeRequest.tab.id;
						setCloseRequest(null);
						closeTabNow(id);
					}}
				>
					Discard
				</button>
				<button
					type="button"
					className={dialogPrimaryButtonClass}
					onClick={() => void saveAndCloseFile()}
				>
					Save
				</button>
			</Dialog>

			<Dialog
				open={saveConflict !== null}
				title="Overwrite newer file?"
				description={
					saveConflict
						? `“${saveConflict.path.split("/").pop() || saveConflict.path}” changed on disk since you opened it. Overwriting will replace those changes.`
						: undefined
				}
				onClose={() => setSaveConflict(null)}
			>
				<button
					type="button"
					className={dialogButtonClass}
					onClick={() => setSaveConflict(null)}
				>
					Cancel
				</button>
				<button
					type="button"
					className={dialogButtonClass}
					onClick={() => {
						if (!saveConflict) return;
						const path = saveConflict.path;
						setSaveConflict(null);
						void reloadDocument(path, false);
					}}
				>
					Reload from disk
				</button>
				<button
					type="button"
					className={dialogPrimaryButtonClass}
					onClick={() => void overwriteChangedFile()}
				>
					Overwrite
				</button>
			</Dialog>

			<Dialog
				open={workspaceEditRequest !== null}
				title={workspaceEditRequest?.label ?? "Apply workspace edit?"}
				description={
					workspaceEditRequest
						? `${workspaceEditRequest.summary.edits} ${workspaceEditRequest.summary.edits === 1 ? "edit" : "edits"} across ${workspaceEditRequest.summary.files.length} ${workspaceEditRequest.summary.files.length === 1 ? "file" : "files"}. The files will be saved together after their disk revisions are checked.`
						: undefined
				}
				onClose={closeWorkspaceEditPreview}
			>
				{workspaceEditRequest && (
					<div className="mr-auto max-h-36 min-w-0 overflow-auto text-[11px] text-fg-muted">
						{workspaceEditRequest.summary.files.map((path) => (
							<div key={path} className="truncate py-0.5" title={path}>
								{path}
							</div>
						))}
					</div>
				)}
				<button
					type="button"
					className={dialogButtonClass}
					disabled={workspaceEditRequest?.applying}
					onClick={closeWorkspaceEditPreview}
				>
					Cancel
				</button>
				<button
					type="button"
					className={dialogPrimaryButtonClass}
					disabled={workspaceEditRequest?.applying}
					onClick={() => void confirmWorkspaceEdit()}
				>
					{workspaceEditRequest?.applying ? "Applying…" : "Apply and Save"}
				</button>
			</Dialog>

			<Dialog
				open={closeRequest?.kind === "terminal"}
				title="Terminate terminal?"
				description="Closing this tab will stop the shell and any process running in it."
				onClose={() => setCloseRequest(null)}
			>
				<button
					type="button"
					className={dialogButtonClass}
					onClick={() => setCloseRequest(null)}
				>
					Cancel
				</button>
				<button
					type="button"
					className={`${dialogButtonClass} border-danger/40 text-danger hover:bg-danger/10`}
					onClick={() => void terminateTerminal()}
				>
					Terminate
				</button>
			</Dialog>

			<Dialog
				open={sessionDelete !== null}
				title="Delete session?"
				description={
					sessionDelete
						? `“${sessionDelete.title}” and its saved history will be permanently deleted.`
						: undefined
				}
				onClose={() => setSessionDelete(null)}
			>
				<button
					type="button"
					className={dialogButtonClass}
					onClick={() => setSessionDelete(null)}
				>
					Cancel
				</button>
				<button
					type="button"
					className={`${dialogButtonClass} border-danger/40 text-danger hover:bg-danger/10`}
					onClick={() => void confirmSessionDelete()}
				>
					Delete
				</button>
			</Dialog>

			{!connected && (
				<div className="absolute inset-0 z-140 flex items-center justify-center backdrop-blur-md bg-bg/60">
					<div className="flex flex-col items-center gap-3 text-fg-muted">
						<Loader2 size={28} className="animate-spin" />
						<div className="text-[13px]">Reconnecting…</div>
					</div>
				</div>
			)}
		</div>
	);
}

function ManagedToolsFooter({ status }: { status?: ManagedToolsStatus }) {
	const [dismissed, setDismissed] = useState("");
	if (status?.state === "error") {
		const tools = (status.unavailable ?? []).join(", ");
		const key = tools || status.error || "error";
		if (dismissed === key) return null;
		const message = tools
			? `Couldn't install ${tools}. Project and system tools still work.`
			: "Automatic tool setup could not finish. Project and system tools still work.";
		return (
			<div
				data-managed-tools-status
				role="status"
				aria-live="polite"
				aria-atomic="true"
				className="flex h-8 shrink-0 items-center gap-2 border-t border-warning/30 bg-warning/10 px-3 text-[10.5px] text-warning"
			>
				<span
					className="min-w-0 flex-1 truncate"
					title={status.error || message}
				>
					{message}
				</span>
				<button
					type="button"
					onClick={() => setDismissed(key)}
					className="shrink-0 px-1 opacity-70 hover:opacity-100"
					aria-label="Dismiss"
				>
					×
				</button>
			</div>
		);
	}
	if (status?.state !== "installing") return null;

	const message = status.label
		? `Setting up ${status.label}…`
		: "Checking tools…";
	const progress =
		status.current && status.total ? `${status.current}/${status.total}` : "";
	return (
		<div
			data-managed-tools-status
			role="status"
			aria-live="polite"
			aria-atomic="true"
			className="flex h-8 shrink-0 items-center gap-2 border-t border-border-subtle bg-bg-surface/20 px-3 text-[10.5px] text-fg-dim"
		>
			<Loader2 size={11} className="shrink-0 animate-spin text-accent" />
			<span className="min-w-0 flex-1 truncate" title={message}>
				{message}
			</span>
			{progress && <span className="shrink-0 tabular-nums">{progress}</span>}
		</div>
	);
}

function formatTokens(n: number): string {
	if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
	if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
	return String(n);
}

function debugOperationLabel(operation: DebugOperation): string {
	switch (operation) {
		case "continue":
			return "continue debugging";
		case "next":
			return "step over";
		case "stepIn":
			return "step into";
		case "stepOut":
			return "step out";
		case "stepBack":
			return "step back";
		case "pause":
			return "pause debugging";
		case "stop":
			return "stop debugging";
	}
}

function ResizeHandle({ label, hidden }: { label: string; hidden: boolean }) {
	return (
		<Separator
			aria-label={label}
			disabled={hidden}
			className={`relative z-20 -mx-1.5 w-3 shrink-0 cursor-col-resize bg-transparent outline-none ${
				hidden ? "pointer-events-none" : ""
			}`}
		/>
	);
}

function estimateStreamingTokens(entries: ChatEntry[]): number {
	let chars = 0;
	for (let i = entries.length - 1; i >= 0; i--) {
		const e = entries[i];
		if (e.type !== "reasoning" && e.type !== "assistant") break;
		chars += e.content.length;
	}
	return Math.floor(chars / 4);
}

function isClosableTab(tab: CenterTab): boolean {
	return tab.type !== "chat" || !!tab.sessionId;
}

function TabCrashed({
	error,
	errorInfo,
}: {
	error: Error;
	errorInfo: ErrorInfo | null;
}) {
	return (
		<ErrorPanel
			title="This tab stopped rendering"
			error={error}
			errorInfo={errorInfo}
		/>
	);
}

function TabContextMenu({
	x,
	y,
	tabId,
	tabCount,
	preview,
	pane,
	canMoveRight,
	onMove,
	onClose,
	onKeepOpen,
	onCloseTab,
	onCloseOthers,
	onCloseAll,
}: {
	x: number;
	y: number;
	tabId?: string;
	tabCount: number;
	preview: boolean;
	pane: PaneSide;
	canMoveRight: boolean;
	onMove: (id: string, side: PaneSide) => void;
	onClose: () => void;
	onKeepOpen: (id: string) => void;
	onCloseTab: (id: string) => void;
	onCloseOthers: (id: string) => void;
	onCloseAll: () => void;
}) {
	const items: { label: string; disabled?: boolean; run: () => void }[] = [];
	if (tabId) {
		if (preview) {
			items.push({ label: "Keep Open", run: () => onKeepOpen(tabId) });
		}
		if (pane === "right") {
			items.push({ label: "Move Left", run: () => onMove(tabId, "left") });
		} else if (canMoveRight) {
			items.push({ label: "Move Right", run: () => onMove(tabId, "right") });
		}
		items.push({ label: "Close", run: () => onCloseTab(tabId) });
		items.push({
			label: "Close Others",
			disabled: tabCount < 2,
			run: () => onCloseOthers(tabId),
		});
	}
	items.push({ label: "Close All", run: onCloseAll });

	return (
		<FloatingMenu
			open
			onOpenChange={(open) => !open && onClose()}
			reference={{ x, y }}
			label="Tab actions"
			className="z-[100] min-w-[160px] rounded-md border border-border bg-bg-elevated/95 py-1 shadow-xl backdrop-blur-sm"
		>
			{items.map((item) => (
				<button
					type="button"
					role="menuitem"
					key={item.label}
					disabled={item.disabled}
					onClick={() => {
						onClose();
						item.run();
					}}
					className="flex w-full items-center px-3 py-1.5 text-left text-[11.5px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg disabled:cursor-default disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-fg-muted"
				>
					{item.label}
				</button>
			))}
		</FloatingMenu>
	);
}

function WorkspaceTabButton({
	active,
	onClick,
	children,
}: {
	active: boolean;
	onClick: () => void;
	children: React.ReactNode;
}) {
	return (
		<button
			type="button"
			role="tab"
			aria-selected={active}
			tabIndex={active ? 0 : -1}
			className={`relative flex cursor-pointer items-center px-2.5 text-[12px] font-medium transition-colors ${
				active ? "text-fg" : "text-fg-dim hover:text-fg-muted"
			}`}
			onClick={onClick}
			onKeyDown={(event) => {
				if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
				const tabs = Array.from(
					event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>(
						'[role="tab"]',
					) ?? [],
				);
				const index = tabs.indexOf(event.currentTarget);
				const offset = event.key === "ArrowLeft" ? -1 : 1;
				const next = tabs[(index + offset + tabs.length) % tabs.length];
				event.preventDefault();
				next?.focus();
				next?.click();
			}}
		>
			{children}
			{active && (
				<span className="absolute right-2.5 bottom-0 left-2.5 h-[2px] rounded-full bg-accent" />
			)}
		</button>
	);
}

function TerminalLauncher({
	shells,
	onCreate,
}: {
	shells: ShellEntry[];
	onCreate: (shell?: string) => void;
}) {
	const [open, setOpen] = useState(false);
	const launcherRef = useRef<HTMLDivElement>(null);

	const label = terminalShellName(shells[0]?.name ?? "shell");
	const hasChoices = shells.length > 1;

	return (
		<div
			ref={launcherRef}
			className="relative self-center flex h-8 w-8 shrink-0"
		>
			<button
				type="button"
				className="flex h-8 w-8 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
				onClick={(event) => {
					if (event.altKey && hasChoices) {
						setOpen(true);
						return;
					}
					onCreate();
				}}
				title={`New ${label} terminal${hasChoices ? ` · ${TERMINAL_SHELL_MENU_HINT} to choose shell` : ""}`}
				aria-label={`New ${label} terminal`}
				aria-haspopup={hasChoices ? "menu" : undefined}
				aria-expanded={hasChoices ? open : undefined}
			>
				<SquareTerminal size={13} />
			</button>
			<FloatingMenu
				open={open}
				onOpenChange={setOpen}
				reference={launcherRef.current}
				placement="bottom-end"
				label="Terminal shell"
				maxHeight={220}
				className="z-[100] min-w-[160px] overflow-y-auto rounded-md border border-border bg-bg-elevated/95 py-1 shadow-xl backdrop-blur-sm"
			>
				{shells.map((shell, index) => (
					<button
						type="button"
						role="menuitem"
						key={shell.id}
						onClick={() => {
							setOpen(false);
							onCreate(shell.id);
						}}
						className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[11.5px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
					>
						<SquareTerminal size={12} className="shrink-0 text-fg-dim" />
						<span className="flex-1 truncate">{shell.name}</span>
						{index === 0 && (
							<span className="text-[10px] text-fg-dim">default</span>
						)}
					</button>
				))}
			</FloatingMenu>
		</div>
	);
}

function UsageIndicator({
	inputTokens,
	cachedTokens,
	outputTokens,
	lastInputTokens,
	contextWindow,
	outputEstimated,
}: {
	inputTokens: number;
	cachedTokens: number;
	outputTokens: number;
	lastInputTokens: number;
	contextWindow: number;
	outputEstimated: boolean;
}) {
	const [open, setOpen] = useState(false);
	const buttonRef = useRef<HTMLButtonElement>(null);
	const hasContext = contextWindow > 0 && lastInputTokens > 0;
	const used = hasContext ? Math.min(lastInputTokens, contextWindow) : 0;
	const usedPercent = hasContext ? Math.round((used / contextWindow) * 100) : 0;
	const leftPercent = hasContext ? Math.max(0, 100 - usedPercent) : 0;
	const freeTokens = hasContext ? Math.max(0, contextWindow - used) : 0;
	const tone = !hasContext
		? "text-fg-dim"
		: leftPercent <= 10
			? "text-danger"
			: leftPercent <= 30
				? "text-warning"
				: "text-fg-dim";

	const hoverSummary = hasContext
		? `${leftPercent}% context left · ${formatTokens(used)} of ${formatTokens(contextWindow)} used · ↑${formatTokens(inputTokens)} · ↓${outputEstimated ? "~" : ""}${formatTokens(outputTokens)}`
		: `↑${formatTokens(inputTokens)} input · ↓${outputEstimated ? "~" : ""}${formatTokens(outputTokens)} output`;

	return (
		<>
			<button
				ref={buttonRef}
				type="button"
				className={`flex h-full shrink-0 items-center border-l border-border-subtle bg-bg px-2 text-[11px] tabular-nums transition-colors hover:bg-bg-hover ${tone}`}
				title={hoverSummary}
				aria-label={hasContext ? `${leftPercent}% context left` : "Token usage"}
				aria-haspopup="dialog"
				aria-expanded={open}
				onClick={() => setOpen((value) => !value)}
			>
				{hasContext ? `${leftPercent}%` : "Usage"}
			</button>
			<FloatingSurface
				open={open}
				onOpenChange={setOpen}
				reference={buttonRef.current}
				placement="bottom-end"
				gap={6}
				role="dialog"
				label="Usage information"
				className="z-[100] w-72 rounded-lg border border-border bg-bg-elevated p-3 shadow-2xl"
			>
				<div className="text-[12px] font-medium text-fg">Context usage</div>
				{hasContext ? (
					<>
						<div className="mt-1 text-[11px] text-fg-muted">
							{formatTokens(used)} / {formatTokens(contextWindow)} tokens ·{" "}
							{usedPercent}% used
						</div>
						<div className="mt-2 h-1.5 overflow-hidden rounded-full bg-bg-active">
							<div
								className={`h-full rounded-full ${leftPercent <= 10 ? "bg-danger" : leftPercent <= 30 ? "bg-warning" : "bg-accent"}`}
								style={{ width: `${usedPercent}%` }}
							/>
						</div>
					</>
				) : (
					<div className="mt-1 text-[11px] text-fg-dim">
						Context-window data is unavailable for this session.
					</div>
				)}
				<dl className="mt-3 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1.5 text-[11px]">
					{hasContext && (
						<>
							<dt className="text-fg-dim">Free space</dt>
							<dd className="text-right font-mono text-fg-muted">
								{freeTokens.toLocaleString()} ({leftPercent}%)
							</dd>
						</>
					)}
					<dt className="text-fg-dim">Input tokens</dt>
					<dd className="text-right font-mono text-fg-muted">
						{inputTokens.toLocaleString()}
					</dd>
					<dt className="text-fg-dim">Cached input</dt>
					<dd className="text-right font-mono text-fg-muted">
						{cachedTokens.toLocaleString()}
					</dd>
					<dt className="text-fg-dim">Output tokens</dt>
					<dd className="text-right font-mono text-fg-muted">
						{outputEstimated ? "~" : ""}
						{outputTokens.toLocaleString()}
					</dd>
				</dl>
			</FloatingSurface>
		</>
	);
}
