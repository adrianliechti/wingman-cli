import { useMainLayout } from "./hooks/useMainLayout.ts";
import { useMobileLayout } from "./hooks/useMobileLayout.ts";
import { MobileNavigation } from "./components/MobileNavigation";
import {
	useWorkspace,
	useSessionSettings,
	draftSettingsKey,
} from "./state/workspaceContext.ts";
import { ComposerDraft } from "./state/composerDraft.ts";
import { isDraft, sessionKey, splitSessionKey } from "./state/sessionStore.ts";
import { workspaceClient } from "./state/workspaceClient.ts";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	Bot,
	Bug,
	Compass,
	Code2,
	Eye,
	FileText,
	FilePlus,
	FolderOpen,
	FolderTree,
	GitCompare,
	GitCompareArrows,
	Globe2,
	History,
	Lightbulb,
	Loader2,
	type LucideIcon,
	MessageSquare,
	MessageSquarePlus,
	Menu as MenuIcon,
	Monitor,
	MonitorPlay,
	PanelBottom,
	PanelLeftOpen,
	PanelTop,
	Plus,
	RefreshCw,
	Search,
	Save,
	SaveAll,
	Sparkles,
	SquareTerminal,
	Stethoscope,
	Wrench,
} from "lucide-react";
import {
	type CSSProperties,
	type ErrorInfo,
	type ReactNode,
	useCallback,
	useEffect,
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
import { getInspectAvailability } from "./api/capabilities";
import {
	controlDebug,
	getDebugSession,
	getDebugState,
	type DebugSession,
} from "./api/debug";
import { createWorkspaceFile } from "./api/files";
import { queryKeys } from "./api/query";
import { deleteSession, type SessionInfo } from "./api/sessions";
import {
	setEditorTabCompletion,
	setWindowTerminalPosition,
} from "./api/settings";
import { listSchedules, listTasks } from "./api/tasks";
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
} from "./mainLayout";
import { formatAgentName } from "./utils/agents";
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
import {
	FileTab,
	type EditorSelectionContext,
	type FileTabHandle,
} from "./components/FileTab";
import { InsightsTab } from "./components/insights/InsightsTab";
import { ProblemsPanel } from "./components/ProblemsPanel";
import { TasksPanel } from "./components/TasksPanel";
import { TaskTab } from "./components/TaskTab";
import { TerminalView } from "./components/TerminalView";
import { WorkspaceFilesPanel } from "./components/WorkspaceFilesPanel";
import { WorkspaceActivity } from "./components/WorkspaceActivity";
import {
	Dialog,
	dialogButtonClass,
	dialogPrimaryButtonClass,
	useToast,
} from "./components/ui/Feedback";
import { FloatingMenu, FloatingSurface } from "./components/ui/Floating";
import { AgentSessions } from "./components/AgentSessions";
import { useCapabilities } from "./hooks/useCapabilities";
import { useAutoHidingScrollbars } from "./hooks/useAutoHidingScrollbars";
import { type OpenDocument, useOpenDocuments } from "./hooks/useOpenDocuments";
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
import {
	contextRemainingPercent,
	shouldShowContextIndicator,
} from "./utils/usage";
type WorkspaceTab = "changes" | "files" | "inspect";
type ChatAuxiliaryView = "sessions" | "agents";
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
type FilePathRequest = {
	path: string;
	sourcePath: string;
	sourceTabId: string;
	content: string;
	untitled: boolean;
	closeAfterSave?: boolean;
	submitting: boolean;
	error?: string;
};
const SIDE_PANEL_MIN_SIZE = 240;
const SIDE_PANEL_DEFAULT_SIZE = SIDE_PANEL_MIN_SIZE;
const SIDE_PANEL_MAX_SIZE = 480;
const CENTER_PANEL_MIN_SIZE = 320;
const TERMINAL_PANEL_DEFAULT_SIZE = 240;
const TERMINAL_PANEL_MIN_SIZE = 120;
const RIGHT_PANEL_WIDTH = "min(240px, 50%)";
const DEBUG_DETAILS_MIN_SIZE = 240;
const DEBUG_DETAILS_MAX_SIZE = 480;
// Width per workspace tab below which the label is replaced by its icon.
const WORKSPACE_TAB_LABEL_MIN_WIDTH = 58;

const EMPTY_ENTRIES: never[] = [];
const EMPTY_SHELLS: ShellEntry[] = [];
const EMPTY_CENTER_TAB = {
	id: "",
	type: "empty" as const,
	label: "",
	pane: undefined,
};
const EMPTY_USAGE = {
	inputTokens: 0,
	cachedTokens: 0,
	outputTokens: 0,
	lastInputTokens: 0,
	contextWindow: 0,
};

const IS_MAC =
	window.shell?.platform === "macos" ||
	(!window.shell && /Mac|iPhone|iPad/.test(navigator.platform));
const TERMINAL_SHORTCUT = IS_MAC ? "⌃⌥T" : "Ctrl+Alt+T";

function markdownFenceFor(text: string): string {
	const fenceFor = (marker: "`" | "~") => {
		let longest = 0;
		let current = 0;
		for (const character of text) {
			if (character === marker) {
				current++;
				longest = Math.max(longest, current);
			} else {
				current = 0;
			}
		}
		return marker.repeat(Math.max(3, longest + 1));
	};
	const backticks = fenceFor("`");
	const tildes = fenceFor("~");
	return backticks.length <= tildes.length ? backticks : tildes;
}

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
	useAutoHidingScrollbars();
	const mobile = useMobileLayout();
	const { backend: agentId, selectBackend, drafts } = useWorkspace();
	const client = workspaceClient();
	const [initialTab] = useState<CenterTab>(() => {
		const sid = decodeURIComponent(
			location.pathname.split("/").filter(Boolean)[1] ?? "",
		);
		if (sid) {
			const key = sessionKey(agentId, sid);
			return {
				id: chatTabId(key),
				type: "chat",
				backendId: agentId,
				sessionId: key,
				label: "Session",
			};
		}
		return draftChatTab(agentId);
	});

	const {
		connected,
		sessions,
		sendChat,
		cancel,
		removeQueued,
		updateQueued,
		resumeQueue,
		clearQueue,
		dismissError,
		respondPrompt,
		subscribe,
		observe,
	} = useWebSocket();
	useServerQueryInvalidation(subscribe, connected);
	const queryClient = useQueryClient();
	const toast = useToast();
	const createSessionMutation = useCallback(
		() => client.create(agentId, crypto.randomUUID()),
		[client, agentId],
	);
	const {
		documents,
		dirtyPaths,
		openDocument,
		openCreatedDocument,
		openUntitledDocument,
		updateDraft,
		saveDocument,
		discardDocument,
		reloadDocument,
		closeDocument,
		moveDocuments,
		applyWorkspaceEdit,
	} = useOpenDocuments(subscribe);
	const capabilities = useCapabilities();
	const showChanges = !!(capabilities?.git || capabilities?.git_init);
	const { inspect: showInspect, debug: showDebug } =
		getInspectAvailability(capabilities);
	const showAgents = capabilities?.tasks ?? false;
	const showTerminal = capabilities?.terminal ?? false;
	const tabAvailable = capabilities?.tab ?? false;
	const tabEnabled =
		tabAvailable && (capabilities?.["editor.tab.completion"] ?? false);
	const languageServicesKey = `${capabilities?.lsp ?? false}:${capabilities?.managed_tools?.state ?? ""}`;
	const toggleEditorTabCompletion = useCallback(async () => {
		try {
			await setEditorTabCompletion(!tabEnabled);
			void queryClient.invalidateQueries({
				queryKey: queryKeys.capabilities,
			});
			toast({
				title: `Tab Completion ${tabEnabled ? "disabled" : "enabled"}`,
				description: tabEnabled
					? undefined
					: "Completions use model requests while you type.",
			});
		} catch (error) {
			toast({
				title: "Could not change Tab Completion",
				description: String(error),
				tone: "error",
			});
		}
	}, [queryClient, tabEnabled, toast]);
	const [requestedWorkspaceTab, setRequestedWorkspaceTab] =
		useState<WorkspaceTab>("files");
	const terminalDocked =
		capabilities?.["window.terminal.position"] === "bottom";
	const [activeDockedTerminalId, setActiveDockedTerminalId] = useState("");
	const lastCenterActiveIdRef = useRef(initialTab.id);
	const [problemsRefreshKey, setProblemsRefreshKey] = useState(0);
	const [workspaceSearching, setWorkspaceSearching] = useState(false);
	const [searchFocusKey, setSearchFocusKey] = useState(0);
	const [sidePanelCollapsed, setSidePanelCollapsed] = useState(false);
	const [chatAuxiliaryViews, setChatAuxiliaryViews] = useState<
		Record<string, ChatAuxiliaryView | undefined>
	>({});
	const workspaceTabsRef = useRef<HTMLDivElement>(null);
	// The workspace tablist shares the title bar with the native window
	// controls, so its width is whatever the panel leaves over. Once a tab is too
	// narrow to spell its label out the text is swapped for the icon, which stays
	// recognisable where a truncated word does not.
	const [workspaceTabsCompact, setWorkspaceTabsCompact] = useState(false);
	useEffect(() => {
		const element = workspaceTabsRef.current;
		if (!element) return;
		const updateCompactMode = () => {
			const tabs = element.childElementCount;
			if (tabs === 0) return;
			setWorkspaceTabsCompact(
				element.clientWidth / tabs < WORKSPACE_TAB_LABEL_MIN_WIDTH,
			);
		};
		const observer = new ResizeObserver(updateCompactMode);
		observer.observe(element);
		return () => observer.disconnect();
	}, [sidePanelCollapsed, showChanges, showInspect]);
	const appRef = useRef<HTMLDivElement>(null);
	const sidePanelWidthRef = useRef(SIDE_PANEL_DEFAULT_SIZE);
	const sidePanelRef = usePanelRef();
	const debugDetailsPanelRef = usePanelRef();
	const [debugDetailsWidth, setDebugDetailsWidth] = useState(
		DEBUG_DETAILS_MIN_SIZE,
	);
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

	const {
		tabs,
		activeTabId,
		leftActiveId,
		rightActiveId,
		currentSessionId,
		setTabs,
		setActiveTabId,
		setLeftActiveId,
		setRightActiveId,
		setCurrentSessionId,
	} = useMainLayout({
		tabs: [initialTab],
		activeTabId: initialTab.id,
		leftActiveId: initialTab.id,
		rightActiveId: "",
		currentSessionId: initialTab.sessionId ?? "",
	});

	const fileTabHandlesRef = useRef(new Map<string, FileTabHandle>());
	const [dragTabId, setDragTabId] = useState<string | null>(null);
	const activePaneRef = useRef<"right" | undefined>(undefined);
	const [fileViews, setFileViews] = useState<Record<string, FileView>>({});
	const [paletteOpen, setPaletteOpen] = useState(false);
	const [paletteEditorActions, setPaletteEditorActions] =
		useState<FileTabHandle | null>(null);
	const [composerSeed, setComposerSeed] = useState<{
		text: string;
		files?: string[];
		append?: boolean;
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

	const openWorkspaceEditFiles = useCallback(
		(paths: readonly string[]) => {
			if (paths.length === 0) return;
			const pane = activePaneRef.current;
			const activeFile = tabs.find(
				(tab) => tab.id === activeTabId && tab.type === "file",
			);
			const keepActive = !!activeFile?.path && paths.includes(activeFile.path);
			const firstExisting = tabs.find(
				(tab) => tab.type === "file" && tab.path === paths[0],
			);
			setTabs((current) => {
				const next = [...current];
				for (const path of paths) {
					const index = next.findIndex(
						(tab) => tab.type === "file" && tab.path === path,
					);
					if (index >= 0) {
						if (next[index].preview) {
							next[index] = { ...next[index], preview: undefined };
						}
						continue;
					}
					next.push({
						id: `file:${path}`,
						type: "file",
						label: path.split("/").pop() || path,
						path,
						pane,
					});
				}
				return next;
			});
			// Keep the initiating editor focused. In particular, promoting a preview
			// tab must not create and activate a second editor for the same file.
			if (!keepActive) setActiveTabId(firstExisting?.id ?? `file:${paths[0]}`);
		},
		[activeTabId, tabs, setActiveTabId, setTabs],
	);

	const runWorkspaceEdit = useCallback(
		async (envelope: WorkspaceEditEnvelope, label: string) => {
			const result = await applyWorkspaceEdit(envelope);
			if (!result.ok) {
				toast({
					title: `${label} failed`,
					description: result.error,
					tone: "error",
				});
			} else {
				openWorkspaceEditFiles(result.paths ?? []);
			}
			return result.ok;
		},
		[applyWorkspaceEdit, openWorkspaceEditFiles, toast],
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
				if (terminalDocked) setActiveDockedTerminalId(tab.id);
				else setActiveTabId(tab.id);
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
		[queryClient, toast, terminalDocked, setActiveTabId, setTabs],
	);

	useEffect(() => {
		if (!showTerminal || !terminalsQuery.data) return;
		const entries = terminalsQuery.data;
		const ids = new Set(entries.map((entry) => entry.id));
		const debugTerminalIDs = [...debugTerminalIDsRef.current];
		setTabs((prev) => {
			const next = prev.filter(
				(tab) => tab.type !== "terminal" || ids.has(tab.terminalId ?? ""),
			);
			const known = new Set(
				next.flatMap((tab) => (tab.terminalId ? [tab.terminalId] : [])),
			);
			for (const id of debugTerminalIDs) known.add(id);
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
	}, [showTerminal, terminalsQuery.data, setTabs]);

	useEffect(() => {
		const onKey = (e: KeyboardEvent) => {
			// Ctrl/Cmd+P matches the TUI command center (and suppresses the
			// browser print dialog); Cmd+K stays as the web-app convention.
			if (
				(e.metaKey || e.ctrlKey) &&
				["k", "p"].includes(e.key.toLowerCase())
			) {
				e.preventDefault();
				e.stopPropagation();
				if (paletteOpen) {
					setPaletteOpen(false);
					setPaletteEditorActions(null);
				} else {
					const editorActions = fileTabHandlesRef.current.get(activeTabId);
					setPaletteEditorActions(
						editorActions?.hasSelection() ? editorActions : null,
					);
					setPaletteOpen(true);
				}
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
		// Capture the palette shortcut before embedded editors can consume it as
		// the start of one of their own key chords.
		window.addEventListener("keydown", onKey, true);
		return () => window.removeEventListener("keydown", onKey, true);
	}, [activeTabId, createTerminal, paletteOpen]);

	const terminalTabs = tabs.filter((tab) => tab.type === "terminal");
	const centerTabs = terminalDocked
		? tabs.filter((tab) => tab.type !== "terminal")
		: tabs;
	const activeTab =
		centerTabs.find((tab) => tab.id === activeTabId) ??
		centerTabs[0] ??
		EMPTY_CENTER_TAB;
	const activeTaskSessionId =
		activeTab.type === "chat" &&
		activeTab.sessionId &&
		!isDraft(activeTab.sessionId)
			? activeTab.sessionId
			: "";
	const activeTasksQuery = useQuery({
		queryKey: queryKeys.tasks.list(activeTaskSessionId),
		enabled: showAgents && !!activeTaskSessionId,
		queryFn: ({ signal }) => listTasks(activeTaskSessionId, signal),
		refetchInterval: (query) =>
			query.state.data?.some((task) => task.status === "running")
				? 3000
				: query.state.data?.length === 0
					? 3000
					: false,
	});
	const activeSchedulesQuery = useQuery({
		queryKey: queryKeys.tasks.schedules(activeTaskSessionId),
		enabled: showAgents && !!activeTaskSessionId,
		queryFn: ({ signal }) => listSchedules(activeTaskSessionId, signal),
		refetchInterval: (query) =>
			(query.state.data?.length ?? 0) > 0 ? 30000 : false,
	});
	const activeTasks = activeTasksQuery.data ?? [];
	const activeSchedules = activeSchedulesQuery.data ?? [];
	const activeBackgroundAgentActivity = {
		available: activeTasks.length > 0 || activeSchedules.length > 0,
		working: activeTasks.some((task) => task.status === "running"),
	};
	const activeTaskPhase = activeTaskSessionId
		? sessions[activeTaskSessionId]?.phase
		: undefined;
	useEffect(() => {
		if (!showAgents || !activeTaskSessionId || !activeTaskPhase) return;
		void queryClient.invalidateQueries({
			queryKey: queryKeys.tasks.session(activeTaskSessionId),
		});
	}, [activeTaskPhase, activeTaskSessionId, queryClient, showAgents]);
	const activeDockedTerminal =
		terminalTabs.find((tab) => tab.id === activeDockedTerminalId) ??
		terminalTabs[0];
	const activeDockedTerminalTabId = activeDockedTerminal?.id ?? "";
	useEffect(() => {
		const selected = tabs.find((tab) => tab.id === activeTabId);
		if (selected && selected.type !== "terminal") {
			lastCenterActiveIdRef.current = activeTab.id;
		}
	}, [activeTab.id, activeTabId, tabs]);
	const leftTabs = centerTabs.filter((tab) => paneOf(tab) === "left");
	const rightTabs = leftTabs.length
		? centerTabs.filter((tab) => paneOf(tab) === "right")
		: [];
	const rightTab =
		(activeTab.pane === "right"
			? rightTabs.find((tab) => tab.id === activeTab.id)
			: undefined) ??
		rightTabs.find((tab) => tab.id === rightActiveId) ??
		rightTabs[0];
	const leftPool = leftTabs.length ? leftTabs : centerTabs;
	const leftTab =
		(activeTab.pane !== "right"
			? leftPool.find((tab) => tab.id === activeTab.id)
			: undefined) ??
		leftPool.find((tab) => tab.id === leftActiveId) ??
		leftPool[0] ??
		(activeTab.id ? activeTab : undefined);
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
		activeTab.type === "chat"
			? activeTab.sessionId || sessionKey(activeTab.backendId ?? agentId, "")
			: currentSessionId || sessionKey(agentId, "");
	const workspaceTab =
		(requestedWorkspaceTab === "changes" && !showChanges) ||
		(requestedWorkspaceTab === "inspect" && !showInspect)
			? "files"
			: requestedWorkspaceTab;

	const activeSession = sessionId ? sessions[sessionId] : undefined;
	const entries = activeSession?.entries ?? EMPTY_ENTRIES;
	const phase = activeSession?.phase ?? "idle";
	const usage = activeSession?.usage ?? EMPTY_USAGE;

	useEffect(() => {
		const ref = splitSessionKey(sessionId);
		const path = ref.sessionId
			? `/${encodeURIComponent(ref.backendId)}/${encodeURIComponent(ref.sessionId)}`
			: `/${encodeURIComponent(agentId)}`;
		if (location.pathname !== path) window.history.replaceState(null, "", path);
	}, [agentId, sessionId]);
	const observedKeys = JSON.stringify(
		tabs
			.filter((tab) => tab.type === "chat" && tab.sessionId)
			.map((tab) => tab.sessionId!),
	);
	useEffect(() => observe(JSON.parse(observedKeys)), [observe, observedKeys]);

	const streamEstimate =
		phase !== "idle" ? estimateStreamingTokens(entries) : 0;
	const outputTokens = usage.outputTokens + streamEstimate;

	const activateTab = useCallback(
		(tab: CenterTab) => {
			setActiveTabId(tab.id);
			if (tab.type === "chat") {
				setCurrentSessionId(tab.sessionId ?? "");
				selectBackend(
					tab.backendId ??
						(tab.sessionId
							? splitSessionKey(tab.sessionId).backendId
							: agentId),
				);
			}
		},
		[agentId, selectBackend, setActiveTabId, setCurrentSessionId],
	);
	const toggleChatAuxiliary = useCallback(
		(tabId: string, view: ChatAuxiliaryView) => {
			setChatAuxiliaryViews((current) =>
				current[tabId] === view ? {} : { [tabId]: view },
			);
		},
		[],
	);
	const showActiveChatAuxiliary = useCallback(
		(view: ChatAuxiliaryView) => {
			const tab =
				activeTab.type === "chat"
					? activeTab
					: (tabs.find(
							(candidate) =>
								candidate.type === "chat" &&
								candidate.sessionId === currentSessionId,
						) ?? tabs.find((candidate) => candidate.type === "chat"));
			if (!tab) return;
			activateTab(tab);
			setChatAuxiliaryViews({ [tab.id]: view });
		},
		[activateTab, activeTab, currentSessionId, tabs],
	);
	const keepTab = useCallback(
		(id: string) => {
			setTabs((current) =>
				current.map((tab) =>
					tab.id === id && tab.preview ? { ...tab, preview: undefined } : tab,
				),
			);
		},
		[setTabs],
	);
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
			setTabs(placement.tabs);
			activateTab(candidate);
		},
		[activateTab, activeTab.pane, closeDocument, dirtyPaths, tabs, setTabs],
	);

	const openChatTab = useCallback(
		(sid: string, disposition: TabDisposition = "keep") => {
			const existing = tabs.find(
				(t) => t.type === "chat" && t.sessionId === sid,
			);
			if (existing) {
				showCenterTab(existing, disposition);
				return;
			}

			const tab: CenterTab = {
				id: chatTabId(sid),
				type: "chat",
				backendId: splitSessionKey(sid).backendId,
				label: "Session",
				sessionId: sid,
			};
			showCenterTab(tab, disposition);
		},
		[showCenterTab, tabs],
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
		[activateTab, keepTab, openDocument, showCenterTab, tabs, setTabs],
	);

	const openUntitledFile = useCallback(() => {
		const usedLabels = new Set(tabs.map((tab) => tab.label));
		let sequence = 1;
		let label = "Untitled";
		while (usedLabels.has(label)) {
			sequence++;
			label = `Untitled ${sequence}`;
		}
		const path = `untitled:${crypto.randomUUID()}`;
		openUntitledDocument(path);
		showCenterTab({ id: `file:${path}`, type: "file", label, path }, "keep");
	}, [openUntitledDocument, showCenterTab, tabs]);

	const handleFileMove = useCallback(
		(from: string, to: string) => {
			moveDocuments(from, to);
			const movedTabs = new Map(
				tabs.map((tab) => [tab.id, moveWorkspaceTab(tab, from, to)]),
			);
			setTabs((current) =>
				current.map((tab) => moveWorkspaceTab(tab, from, to)),
			);
			setActiveTabId(movedTabs.get(activeTabId)?.id ?? activeTabId);
			setLeftActiveId(movedTabs.get(leftActiveId)?.id ?? leftActiveId);
			setRightActiveId(movedTabs.get(rightActiveId)?.id ?? rightActiveId);
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
		[
			moveDocuments,
			tabs,
			setRightActiveId,
			setLeftActiveId,
			rightActiveId,
			setActiveTabId,
			activeTabId,
			setTabs,
			leftActiveId,
		],
	);

	const openTask = useCallback(
		(task: TaskEntry) => {
			if (!sessionId) return;
			const id = `task:${sessionId}:${task.id}`;
			const pane = activePaneRef.current;
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
								pane,
							},
						],
			);
			setActiveTabId(id);
		},
		[sessionId, setActiveTabId, setTabs],
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
	const invalidateDebugDetails = useCallback(() => {
		void queryClient.invalidateQueries({
			queryKey: queryKeys.debug.inspection,
			exact: true,
		});
		void queryClient.invalidateQueries({
			queryKey: queryKeys.debug.output,
			exact: true,
		});
	}, [queryClient]);
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
			const pane = activePaneRef.current;
			setTabs((current) => syncDebugTab(current, terminalId, active, pane));
			if (!terminalId)
				setDebugContentView((view) => (view === "terminal" ? "output" : view));
			queryClient.setQueryData(queryKeys.debug.session, { session });
		},
		[queryClient, setTabs],
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
			// oxlint-disable-next-line react/set-state-in-effect -- Synchronize the external debugger with tabs, terminal state, and the query cache.
			applyDebugSession(undefined);
			return;
		}
		if (debugStateQuery.data) {
			// oxlint-disable-next-line react/set-state-in-effect -- Synchronize the external debugger with tabs, terminal state, and the query cache.
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
					invalidateDebugDetails();
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
		[
			applyDebugSession,
			debugControlBusy,
			debugSession,
			invalidateDebugDetails,
			queryClient,
			toast,
		],
	);

	const closeTabNow = useCallback(
		(id: string) => {
			const idx = tabs.findIndex((t) => t.id === id);
			if (idx < 0) return;
			const closing = tabs[idx];
			if (closing.type === "file" && closing.path) closeDocument(closing.path);
			setTabs((prev) => prev.filter((tab) => tab.id !== id));
			setChatAuxiliaryViews((current) => {
				if (!(id in current)) return current;
				const next = { ...current };
				delete next[id];
				return next;
			});
			setFileViews((prev) => {
				if (!(id in prev)) return prev;
				const next = { ...prev };
				delete next[id];
				return next;
			});
			if (activeTabId === id) {
				const remaining = tabs.filter((t) => t.id !== id);
				const navigable = terminalDocked
					? remaining.filter((tab) => tab.type !== "terminal")
					: remaining;
				const paneIdx = tabs
					.filter(
						(tab) =>
							(!terminalDocked || tab.type !== "terminal") &&
							paneOf(tab) === paneOf(closing),
					)
					.findIndex((t) => t.id === id);
				const paneTabs = navigable.filter(
					(tab) => paneOf(tab) === paneOf(closing),
				);
				const fallback =
					paneTabs[Math.min(paneIdx, paneTabs.length - 1)] ??
					navigable[Math.min(idx, navigable.length - 1)];
				if (fallback) activateTab(fallback);
				else {
					setActiveTabId("");
					setCurrentSessionId("");
				}
			}
		},
		[
			tabs,
			activeTabId,
			activateTab,
			closeDocument,
			terminalDocked,
			setActiveTabId,
			setCurrentSessionId,
			setTabs,
		],
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

	const requestSaveAs = useCallback(
		(tab: CenterTab, document: OpenDocument, closeAfterSave = false) => {
			if (!tab.path) return;
			setFilePathRequest({
				path: document.untitled ? "" : tab.path,
				sourcePath: tab.path,
				sourceTabId: tab.id,
				content: document.draft,
				untitled: document.untitled,
				closeAfterSave: closeAfterSave || undefined,
				submitting: false,
			});
		},
		[],
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

		if (!request.untitled && path === request.sourcePath) {
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
			const created = await createWorkspaceFile(path, {
				content: request.content,
			});
			if (!created) throw new Error("The created file response was empty.");
			if (request.closeAfterSave) {
				closeTabNow(request.sourceTabId);
				setFilePathRequest(null);
				return;
			}

			openCreatedDocument(created);
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
	}, [
		closeDocument,
		closeTabNow,
		filePathRequest,
		openCreatedDocument,
		saveFile,
		setTabs,
		setActiveTabId,
		setRightActiveId,
		setLeftActiveId,
	]);

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
					openUntitledFile();
					break;
				case "open-folder":
					if (dirtyPaths.size > 0) setOpenFolderRequest(true);
					else void openFolder();
					break;
				case "save":
					if (canSaveFile && activeTab.path && activeDocument) {
						if (activeDocument.untitled) {
							requestSaveAs(activeTab, activeDocument);
						} else {
							void saveFile(activeTab.path);
						}
					}
					break;
				case "save-as":
					if (canSaveFile && activeTab.path && activeDocument) {
						requestSaveAs(activeTab, activeDocument);
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
		openUntitledFile,
		openFolder,
		requestSaveAs,
		saveFile,
	]);

	const requestCloseTab = useCallback(
		async (id: string) => {
			const tab = tabs.find((item) => item.id === id);
			if (!tab) return;
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
				tabId: tab?.id,
			});
		},
		[tabs, setTabMenu],
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
		[activateTab, leftTabs, tabs, setTabs],
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
		[dragTabId, moveTabToPane, tabs, setTabs],
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

	// Windows owns its app menu in the web title bar, so implement the same
	// primary-modifier commands that the native macOS File menu dispatches. Use
	// capture so Monaco and browser defaults cannot consume them first.
	useEffect(() => {
		const onKey = (event: KeyboardEvent) => {
			if (window.shell?.platform === "macos") return;
			const primary = IS_MAC
				? event.metaKey && !event.ctrlKey
				: event.ctrlKey && !event.metaKey;
			if (!primary || event.altKey) return;

			const key = event.key.toLowerCase();
			let command: "new-file" | "open-folder" | "save" | "save-as" | null =
				null;
			let enabled = true;

			if (key === "n" && !event.shiftKey) {
				command = "new-file";
				enabled = !workspaceSwitching;
			} else if (key === "o" && !event.shiftKey) {
				command = "open-folder";
				enabled = !workspaceSwitching;
			} else if (key === "s") {
				command = event.shiftKey ? "save-as" : "save";
				enabled = canSaveFile;
			}

			if (!command) return;
			event.preventDefault();
			event.stopPropagation();
			if (enabled) {
				window.dispatchEvent(
					new CustomEvent("shell:command", { detail: command }),
				);
			}
		};
		window.addEventListener("keydown", onKey, true);
		return () => window.removeEventListener("keydown", onKey, true);
	}, [canSaveFile, workspaceSwitching]);

	const terminateTerminal = useCallback(async () => {
		const request = closeRequest;
		if (request?.kind !== "terminal" || !request.tab.terminalId) return;
		await closeTerminal(request.tab);
	}, [closeRequest, closeTerminal]);

	const saveAndCloseFile = useCallback(async () => {
		const request = closeRequest;
		if (request?.kind !== "file" || !request.tab.path) return;
		const document = documents[request.tab.path];
		if (document?.untitled) {
			setCloseRequest(null);
			requestSaveAs(request.tab, document, true);
			return;
		}
		await saveFile(request.tab.path, request.tab.id);
	}, [closeRequest, documents, requestSaveAs, saveFile]);

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

	const setTerminalTitle = useCallback(
		(id: string, title: string) => {
			if (!title) return;
			setTabs((prev) =>
				prev.map((tab) =>
					tab.type === "terminal" && tab.terminalId === id
						? { ...tab, label: title }
						: tab,
				),
			);
		},
		[setTabs],
	);

	useEffect(() => {
		activePaneRef.current = activeTab.pane;
	}, [activeTab.pane]);

	const openDraft = useCallback(
		(backend: string) => {
			const tab =
				tabs.find(
					(tab) =>
						tab.type === "chat" && !tab.sessionId && tab.backendId === backend,
				) ?? draftChatTab(backend);
			setTabs((current) =>
				current.some((item) => item.id === tab.id)
					? current
					: [...current, tab],
			);
			setActiveTabId(tab.id);
		},
		[tabs, setActiveTabId, setTabs],
	);
	const handleNewSession = useCallback(
		(requestedBackend?: string) => {
			const backend =
				requestedBackend ??
				(activeTab.type === "chat"
					? (activeTab.backendId ??
						(activeTab.sessionId
							? splitSessionKey(activeTab.sessionId).backendId
							: agentId))
					: currentSessionId
						? splitSessionKey(currentSessionId).backendId
						: agentId);
			const tab = draftChatTab(backend);
			selectBackend(backend);
			setTabs((current) => [...current, tab]);
			activateTab(tab);
		},
		[activateTab, activeTab, agentId, currentSessionId, selectBackend, setTabs],
	);
	const handleBackendSelect = useCallback(
		(backend: string) => {
			selectBackend(backend);
			if (activeTab.type === "chat" && !activeTab.sessionId) {
				setTabs((current) =>
					current.map((tab) =>
						tab.id === activeTab.id ? { ...tab, backendId: backend } : tab,
					),
				);
				return;
			}
			openDraft(backend);
		},
		[activeTab, openDraft, selectBackend, setTabs],
	);

	const handleSessionDeleted = useCallback(
		(id: string) => {
			setCurrentSessionId((prev) => (prev === id ? "" : prev));
			const tab = tabs.find((t) => t.type === "chat" && t.sessionId === id);
			if (tab) closeTabNow(tab.id);
		},
		[tabs, closeTabNow, setCurrentSessionId],
	);

	const confirmSessionDelete = useCallback(async () => {
		if (!sessionDelete) return;
		try {
			await deleteSession(sessionDelete.id);
			const id = sessionDelete.id;
			queryClient.setQueryData(
				queryKeys.sessions.list(splitSessionKey(sessionDelete.id).backendId),
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

	const handleSessionSelect = useCallback(
		(id: string, disposition: TabDisposition = "keep") => {
			selectBackend(splitSessionKey(id).backendId);
			openChatTab(id, disposition);
		},
		[selectBackend, openChatTab],
	);
	const draftCreations = useRef(new Map<string, Promise<string>>());
	const [composerDrafts] = useState(() => new Map<string, ComposerDraft>());
	useEffect(() => {
		const open = new Set(tabs.map((tab) => tab.id));
		for (const id of composerDrafts.keys()) {
			if (!open.has(id)) composerDrafts.delete(id);
		}
	}, [composerDrafts, tabs]);
	const [draftErrors, setDraftErrors] = useState<
		Record<string, string | undefined>
	>({});
	const createSessionForDraft = useCallback(
		(key: string, draftTabId?: string): Promise<string> => {
			const owner = splitSessionKey(key).backendId;
			const draft = draftTabId
				? tabs.find(
						(tab) =>
							tab.id === draftTabId && tab.type === "chat" && !tab.sessionId,
					)
				: tabs.find(
						(tab) =>
							tab.type === "chat" &&
							!tab.sessionId &&
							(tab.backendId ?? agentId) === owner,
					);
			const draftID = draft?.id ?? key;
			const creationKey = JSON.stringify([owner, draftID]);
			const previous = draftCreations.current.get(creationKey);
			if (previous) return previous;
			setDraftErrors((current) =>
				current[creationKey]
					? { ...current, [creationKey]: undefined }
					: current,
			);
			const request = client
				.create(owner, draftID)
				.then(async (id) => {
					const settings = drafts[draftSettingsKey(key, draftID)];
					if (settings && Object.keys(settings).length)
						await client.command(id, { type: "settings", ...settings });
					if (draft) {
						// Keep the composer mounted and preserve current navigation. The
						// draft may have moved, closed, or changed backend during startup.
						setTabs((current) =>
							current.map((tab) =>
								tab.id === draftID &&
								!tab.sessionId &&
								(tab.backendId ?? agentId) === owner
									? { ...tab, sessionId: id }
									: tab,
							),
						);
					} else openChatTab(id);
					return id;
				})
				.catch((error) => {
					setDraftErrors((current) => ({
						...current,
						[creationKey]: String(error),
					}));
					throw error;
				});
			draftCreations.current.set(creationKey, request);
			void request.then(
				() => draftCreations.current.delete(creationKey),
				() => draftCreations.current.delete(creationKey),
			);
			return request;
		},
		[client, drafts, tabs, agentId, openChatTab, setTabs],
	);
	const draftInitializationAttempts = useRef(new Set<string>());
	useEffect(() => {
		if (!connected) return;
		// ACP exposes model and mode settings on session/new, not initialize.
		// Allocate when a chat opens, sharing the request with an early send.
		for (const tab of tabs) {
			const backend = tab.backendId ?? agentId;
			if (tab.type !== "chat" || tab.sessionId || backend === "wingman")
				continue;
			const creationKey = JSON.stringify([backend, tab.id]);
			if (draftInitializationAttempts.current.has(creationKey)) continue;
			draftInitializationAttempts.current.add(creationKey);
			void createSessionForDraft(sessionKey(backend, ""), tab.id).catch(() => {
				/* The draft shows the error; sending retries the request. */
			});
		}
	}, [tabs, agentId, connected, createSessionForDraft]);
	useEffect(() => {
		const deleted = new Set(
			Object.values(sessions)
				.filter((session) => session.status === "deleted")
				.map((session) => session.id),
		);
		if (tabs.some((tab) => tab.sessionId && deleted.has(tab.sessionId)))
			setTabs((current) =>
				current.filter((tab) => !tab.sessionId || !deleted.has(tab.sessionId)),
			);
	}, [sessions, tabs, setTabs]);

	const sendForSession = useCallback(
		async (
			key: string,
			text: string,
			files?: string[],
			images?: string[],
			intent: TurnInputIntent = "follow_up",
			draftTabId?: string,
		): Promise<boolean> => {
			try {
				const sid = isDraft(key)
					? await createSessionForDraft(key, draftTabId)
					: key;
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
				if (!(await sendChat(id, command))) {
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

	const askAboutEditorSelection = useCallback(
		(selection: EditorSelectionContext) => {
			focusChat();
			const fence = markdownFenceFor(selection.text);
			const language = /^[a-z0-9_+.-]+$/i.test(selection.language)
				? selection.language
				: "";
			const lines =
				selection.range.start_line === selection.range.end_line
					? `${selection.range.start_line}`
					: `${selection.range.start_line}-${selection.range.end_line}`;
			setComposerSeed({
				text: `Help me with this selection from ${selection.path}:${lines}.\n\n${fence}${language}\n${selection.text}\n${fence}`,
				files: [selection.path],
				append: true,
				nonce: Date.now(),
			});
		},
		[focusChat],
	);
	const consumeComposerSeed = useCallback((nonce: number) => {
		setComposerSeed((current) => (current?.nonce === nonce ? null : current));
	}, []);

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

	const settingsTabId =
		activeTab.type === "chat"
			? activeTab.id
			: tabs.find(
					(tab) =>
						tab.type === "chat" &&
						!tab.sessionId &&
						(tab.backendId ?? agentId) === splitSessionKey(sessionId).backendId,
				)?.id;
	const { settings: activeSettings, setSettings: setActiveSettings } =
		useSessionSettings(sessionId, settingsTabId);
	const { modes, mode } = activeSettings;
	const selectMode = (mode: string) =>
		setActiveSettings({ mode }).catch((error) => {
			toast({
				title: "Could not change mode",
				description: String(error),
				tone: "error",
			});
		});

	const handleSidePanelResize = useCallback(({ inPixels }: PanelSize) => {
		const collapsed = inPixels < 1;
		setSidePanelCollapsed(collapsed);
		if (collapsed) return;
		const width = Math.round(inPixels);
		sidePanelWidthRef.current = width;
		appRef.current?.style.setProperty("--side-panel-width", `${width}px`);
	}, []);
	const handleRightPaneResize = useCallback(({ inPixels }: PanelSize) => {
		if (inPixels <= 0) return;
		appRef.current?.style.setProperty(
			"--right-pane-width",
			`${Math.round(inPixels)}px`,
		);
	}, []);
	const handleTerminalDockResize = useCallback(({ inPixels }: PanelSize) => {
		if (inPixels <= 0) return;
		appRef.current?.style.setProperty(
			"--terminal-panel-height",
			`${Math.round(inPixels)}px`,
		);
	}, []);
	const handleDebugDetailsResize = useCallback(({ inPixels }: PanelSize) => {
		const visible = inPixels > 0;
		setDebugDetailsVisible(visible);
		if (visible) setDebugDetailsWidth(Math.round(inPixels));
	}, []);
	const toggleSidePanel = useCallback(() => {
		const panel = sidePanelRef.current;
		if (panel?.isCollapsed()) {
			panel.resize(`${sidePanelWidthRef.current}px`);
			setSidePanelCollapsed(false);
		} else {
			panel?.collapse();
			setSidePanelCollapsed(true);
		}
	}, [sidePanelRef]);
	const toggleTerminalPlacement = async () => {
		const dockAtBottom = !terminalDocked;
		try {
			await setWindowTerminalPosition(dockAtBottom ? "bottom" : "tab");
			if (dockAtBottom) {
				const selected = tabs.find((tab) => tab.id === activeTabId);
				if (selected?.type === "terminal") {
					setActiveDockedTerminalId(selected.id);
					const fallback =
						tabs.find((tab) => tab.id === lastCenterActiveIdRef.current) ??
						tabs.find((tab) => tab.type !== "terminal");
					if (fallback) setActiveTabId(fallback.id);
				}
			} else if (activeDockedTerminalTabId) {
				setActiveTabId(activeDockedTerminalTabId);
			}
			void queryClient.invalidateQueries({
				queryKey: queryKeys.capabilities,
			});
		} catch (error) {
			toast({
				title: "Could not change Terminal Position",
				description: String(error),
				tone: "error",
			});
		}
	};
	const showSidePanel = useCallback(
		(tab: WorkspaceTab) => {
			setRequestedWorkspaceTab(tab);
			if (sidePanelRef.current?.isCollapsed()) {
				sidePanelRef.current.resize(`${sidePanelWidthRef.current}px`);
				setSidePanelCollapsed(false);
			}
		},
		[sidePanelRef],
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
		const pane = activePaneRef.current;
		setTabs((current) => syncDebugTab(current, terminalId, true, pane));
		if (terminalId) setDebugContentView("terminal");
		showDebugDetails();
		setActiveTabId("debug");
	}, [debugSession, showDebugDetails, setActiveTabId, setTabs]);
	const showDebugSession = useCallback(
		(session: DebugSession) => {
			if (session.terminal_id)
				exitedDebugTerminalIDsRef.current.delete(session.terminal_id);
			applyDebugSession(session);
			void queryClient.invalidateQueries({
				queryKey: queryKeys.debug.state,
				exact: true,
			});
			invalidateDebugDetails();
			setDebugContentView(session.terminal_id ? "terminal" : "output");
			showDebugDetails();
			setActiveTabId("debug");
		},
		[
			applyDebugSession,
			invalidateDebugDetails,
			queryClient,
			showDebugDetails,
			setActiveTabId,
		],
	);
	const showDebugFailure = useCallback(
		(session: DebugSession) => {
			applyDebugSession(session);
			invalidateDebugDetails();
			const pane = activePaneRef.current;
			setTabs((current) => syncDebugTab(current, undefined, true, pane));
			setDebugContentView("output");
			showDebugDetails();
			setActiveTabId("debug");
		},
		[
			applyDebugSession,
			invalidateDebugDetails,
			showDebugDetails,
			setActiveTabId,
			setTabs,
		],
	);
	const handleDebugTerminalExit = useCallback(
		(id: string) => {
			exitedDebugTerminalIDsRef.current.add(id);
			setTabs((current) => syncDebugTab(current, undefined, false));
			setDebugContentView("output");
		},
		[setTabs],
	);
	const showWorkspaceSearch = useCallback(() => {
		showSidePanel("files");
		setWorkspaceSearching(true);
		setSearchFocusKey((value) => value + 1);
	}, [showSidePanel]);
	// react-resizable-panels only reports collapse through onResize, which does
	// not fire for the initial layout. Sync the panel from its real state
	// once mounted so titlebar controls (like the reopen button) render before
	// the first manual resize.
	useEffect(() => {
		setSidePanelCollapsed(sidePanelRef.current?.isCollapsed() ?? false);
	}, [sidePanelRef]);
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

	const paletteActions: PaletteAction[] = [
		...(paletteEditorActions
			? [
					{
						id: "editor.chat-about-selection",
						label: "Chat about this…",
						hint: "Selected text",
						icon: <MessageSquare size={12} className="text-fg-dim shrink-0" />,
						run: () => void paletteEditorActions.chatAboutSelection(),
					},
					{
						id: "editor.transform-selection",
						label: "Transform selection…",
						hint: "Selected text",
						icon: <Sparkles size={12} className="text-fg-dim shrink-0" />,
						run: () => void paletteEditorActions.transformSelection(),
					},
				]
			: []),
		...client.scope.backends.map((backend) => ({
			id: `new-session:${backend.id}`,
			label:
				client.scope.backends.length > 1
					? `New Chat (${formatAgentName(backend.id, backend.name)})`
					: "New Chat",
			icon: <MessageSquarePlus size={12} className="text-fg-dim shrink-0" />,
			run: () => void handleNewSession(backend.id),
		})),
		...(tabs.some((tab) => tab.type === "chat")
			? [
					{
						id: "show-sessions",
						label: "Show sessions",
						icon: <History size={12} className="text-fg-dim shrink-0" />,
						run: () => showActiveChatAuxiliary("sessions"),
					},
				]
			: []),
		{
			id: "toggle-side-panel",
			label: sidePanelCollapsed ? "Show Side Panel" : "Hide Side Panel",
			icon: <PanelLeftOpen size={12} className="text-fg-dim shrink-0" />,
			run: toggleSidePanel,
		},
		...(showChanges
			? [
					{
						id: "show-changes",
						label: "Show changes",
						icon: <GitCompare size={12} className="text-fg-dim shrink-0" />,
						run: () => showSidePanel("changes"),
					},
				]
			: []),
		...(showTerminal
			? [
					...(terminalShells.length === 0
						? [
								{
									id: "new-terminal",
									label: "New Terminal",
									hint: TERMINAL_SHORTCUT,
									icon: (
										<SquareTerminal
											size={12}
											className="text-fg-dim shrink-0"
										/>
									),
									run: () => void createTerminal(),
								},
							]
						: terminalShells.map((shell, index) => ({
								id: `new-terminal-${index}`,
								label: `New Terminal (${terminalShellName(shell.name)})`,
								hint: index === 0 ? TERMINAL_SHORTCUT : undefined,
								icon: (
									<SquareTerminal size={12} className="text-fg-dim shrink-0" />
								),
								run: () => void createTerminal(shell.id),
							}))),
					{
						id: "terminal-placement",
						label: terminalDocked
							? "Show Terminal in Tab"
							: "Show Terminal at Bottom",
						icon: terminalDocked ? (
							<PanelTop size={12} className="text-fg-dim shrink-0" />
						) : (
							<PanelBottom size={12} className="text-fg-dim shrink-0" />
						),
						run: () => void toggleTerminalPlacement(),
					},
				]
			: []),
		...(tabAvailable
			? [
					{
						id: "editor.tab.completion",
						label: `${tabEnabled ? "Disable" : "Enable"} Tab Completion`,
						hint: `${tabEnabled ? "On" : "Off"} · uses model requests while typing`,
						icon: <Sparkles size={12} className="text-fg-dim shrink-0" />,
						run: () => void toggleEditorTabCompletion(),
					},
				]
			: []),
		{
			id: "find-in-files",
			label: "Find in files",
			hint: IS_MAC ? "⇧⌘F" : "Ctrl+Shift+F",
			icon: <Search size={12} className="text-fg-dim shrink-0" />,
			run: showWorkspaceSearch,
		},
		{
			id: "code-graph",
			label: "Open insights",
			icon: <Lightbulb size={12} className="text-fg-dim shrink-0" />,
			run: openInsightsTab,
		},
		{
			id: "show-files",
			label: "Show files",
			icon: <FileText size={12} className="text-fg-dim shrink-0" />,
			run: () => showSidePanel("files"),
		},
		...modes
			.filter((candidate) => candidate.id !== mode)
			.map((candidate) => {
				const Icon = /plan|read|only/i.test(candidate.id) ? Compass : Wrench;
				return {
					id: `mode-${candidate.id}`,
					label: `Switch to ${candidate.name} mode`,
					hint: candidate.description,
					icon: <Icon size={12} className="text-fg-dim shrink-0" />,
					run: () => void selectMode(candidate.id),
				};
			}),
	];

	const runningSessionIds = new Set(
		Object.values(sessions)
			.filter((s) => s.phase !== "idle")
			.map((s) => s.id),
	);

	const chatTabLabel = (tab: CenterTab): string => {
		if (!tab.sessionId) {
			const backend = tab.backendId ?? agentId;
			if (backend === "wingman") return tab.label;
			const definition = client.scope.backends.find(
				(item) => item.id === backend,
			);
			return formatAgentName(backend, definition?.name);
		}
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
		closable: true,
	});
	const leftStripItems: TabStripItem[] = leftPool.map(stripItem);
	const rightStripItems: TabStripItem[] = rightTabs.map(stripItem);

	const renderTabContent = (tab: CenterTab): ReactNode => {
		if (tab.type === "chat") {
			const key = tab.sessionId || sessionKey(tab.backendId ?? agentId, "");
			const backendId = splitSessionKey(key).backendId;
			const backend = client.scope.backends.find(
				(candidate) => candidate.id === backendId,
			);
			const sess = key ? sessions[key] : undefined;
			let composerDraft = composerDrafts.get(tab.id);
			if (!composerDraft) {
				composerDraft = new ComposerDraft();
				composerDrafts.set(tab.id, composerDraft);
			}
			const draftError = !tab.sessionId
				? draftErrors[JSON.stringify([backendId, tab.id])]
				: undefined;
			return (
				<ChatTabLayout
					view={chatAuxiliaryViews[tab.id]}
					sessionId={key}
					backendId={backendId}
					showAgents={showAgents}
					runningSessionIds={runningSessionIds}
					onSessionSelect={(id, disposition) => {
						setChatAuxiliaryViews({ [chatTabId(id)]: "sessions" });
						void handleSessionSelect(id, disposition);
					}}
					onSessionDelete={(id, title) => setSessionDelete({ id, title })}
					onOpenTask={openTask}
				>
					<ChatPanel
						key={tab.id}
						draftId={tab.id}
						draft={composerDraft}
						sessionId={key}
						placeholder={`Message ${formatAgentName(backendId, backend?.name)}…`}
						entries={sess?.entries ?? EMPTY_ENTRIES}
						phase={sess?.phase ?? "idle"}
						onSend={(text, files, images, intent) =>
							sendForSession(key, text, files, images, intent, tab.id)
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
						loading={
							isDraft(key)
								? backendId !== "wingman" && !draftError
								: !sess || sess.status === "loading"
						}
						loadError={
							draftError ?? (sess?.status === "error" ? sess.error : null)
						}
						error={sess?.error ?? null}
						onDismissError={() => {
							if (key) dismissError(key);
						}}
						prompts={sess?.prompts ?? []}
						onPromptReply={(id, reply) => {
							void respondPrompt(key, id, reply);
						}}
						onOpenFile={openFile}
						seed={tab.id === activeTabId ? composerSeed : null}
						onSeedConsumed={consumeComposerSeed}
						toolProgress={sess?.toolProgress ?? {}}
					/>
				</ChatTabLayout>
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
						defaultSize={debugDetailsVisible ? `${debugDetailsWidth}px` : "0px"}
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
		if (tab.type === "terminal") return null;
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
					onDeleted={() => closeTabNow(tab.id)}
				/>
			);
		}
		if (tab.path && documents[tab.path]) {
			const document = documents[tab.path];
			return (
				<FileTab
					key={`${tab.id}:${tab.path}`}
					ref={(handle) => {
						if (handle) fileTabHandlesRef.current.set(tab.id, handle);
						else fileTabHandlesRef.current.delete(tab.id);
					}}
					document={document}
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
						if (document.untitled) {
							requestSaveAs(tab, document);
							return { ok: false };
						}
						return saveFile(tab.path!);
					}}
					onReload={() => void reloadDocument(tab.path!, tab.external ?? false)}
					onOpenFile={openFile}
					onApplyWorkspaceEdit={requestWorkspaceEdit}
					onAskSelection={askAboutEditorSelection}
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
					languageServicesKey={languageServicesKey}
				/>
			);
		}
		return null;
	};

	const renderPane = (tab: CenterTab | undefined): ReactNode => (
		<div
			className="relative h-full min-h-0 min-w-0 overflow-hidden bg-bg"
			onPointerDownCapture={(event) => {
				if (!tab) return;
				if ((event.target as Element).closest("[data-chat-auxiliary-panel]"))
					return;
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
				{tab ? renderTabContent(tab) : <EmptyWorkspace />}
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
						data-mobile-active={activeTab.pane !== "right"}
						minSize="160px"
						className="min-h-0 min-w-0 overflow-hidden"
					>
						{renderPane(leftTab)}
					</Panel>
					<ResizeHandle label="Resize right pane" hidden={false} />
					<Panel
						id="pane-right"
						data-mobile-active={activeTab.pane === "right"}
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

	const sidePanelDocked = !sidePanelCollapsed;
	const collapsedSideTitlebarWidth = 40;
	const activeChatAuxiliary =
		activeTab.type === "chat" ? chatAuxiliaryViews[activeTab.id] : undefined;
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
			{activeTab.type === "chat" && (
				<>
					{showAgents && activeBackgroundAgentActivity.available && (
						<button
							type="button"
							className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors hover:text-fg-muted ${activeChatAuxiliary === "agents" ? "bg-bg-active text-fg" : "text-fg-muted hover:bg-bg-hover"}`}
							onClick={() => toggleChatAuxiliary(activeTab.id, "agents")}
							aria-pressed={activeChatAuxiliary === "agents"}
							title={
								activeChatAuxiliary === "agents"
									? "Hide background tasks"
									: "Show background tasks"
							}
							aria-label={
								activeChatAuxiliary === "agents"
									? "Hide background tasks"
									: "Show background tasks"
							}
						>
							<Bot
								size={13}
								className={
									activeBackgroundAgentActivity.working
										? "animate-pulse text-accent"
										: undefined
								}
							/>
						</button>
					)}
					<button
						type="button"
						className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors hover:text-fg-muted ${activeChatAuxiliary === "sessions" ? "bg-bg-active text-fg" : "text-fg-dim hover:bg-bg-hover"}`}
						onClick={() => toggleChatAuxiliary(activeTab.id, "sessions")}
						aria-pressed={activeChatAuxiliary === "sessions"}
						title={
							activeChatAuxiliary === "sessions"
								? "Hide sessions"
								: "Show sessions"
						}
						aria-label={
							activeChatAuxiliary === "sessions"
								? "Hide sessions"
								: "Show sessions"
						}
					>
						<History size={13} />
					</button>
				</>
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
			<NewItemLauncher
				onNewChat={() => void handleNewSession()}
				onNewTerminal={showTerminal ? () => void createTerminal() : undefined}
				onNewTextFile={workspaceSwitching ? undefined : openUntitledFile}
			/>
			<WorkspaceActivity
				hasLSP={capabilities?.lsp ?? false}
				tools={capabilities?.managed_tools}
			/>
		</div>
	);

	const sidePanelTabs: {
		id: WorkspaceTab;
		icon: LucideIcon;
		label: string;
	}[] = [
		{ id: "files", icon: FolderTree, label: "Files" },
		...(showChanges
			? ([{ id: "changes", icon: GitCompareArrows, label: "Changes" }] as const)
			: []),
		...(showInspect
			? ([{ id: "inspect", icon: Stethoscope, label: "Inspect" }] as const)
			: []),
	];
	const workspaceTabs = (
		<div
			ref={workspaceTabsRef}
			className="flex h-10 w-full min-w-0 shrink-0 items-stretch overflow-hidden"
			role="tablist"
			aria-label="Side panel views"
		>
			{sidePanelTabs.map((tab) => (
				<WorkspaceTabButton
					key={tab.id}
					active={workspaceTab === tab.id}
					icon={tab.icon}
					label={tab.label}
					iconOnly={workspaceTabsCompact}
					onClick={() => setRequestedWorkspaceTab(tab.id)}
				/>
			))}
		</div>
	);
	const sidePanelContent = (
		<aside
			className="flex h-full flex-col bg-transparent"
			aria-label="Side panel"
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
					<div className="flex h-9 shrink-0 items-center px-3">
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
				{workspaceTab === "inspect" ? null : workspaceTab === "changes" &&
				  showChanges ? (
					<DiffsPanel
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
		</aside>
	);
	const renderSidePanelTitlebar = () => {
		return (
			<div
				data-window-interactive
				data-titlebar-side-panel="left"
				data-titlebar-left-panel
				className={`relative z-20 flex shrink-0 items-center overflow-hidden ${sidePanelDocked ? "pl-2" : "gap-1 px-1"}`}
				style={{
					width: sidePanelDocked
						? "calc(var(--side-panel-width) - var(--window-controls-inset) - var(--window-menu-inset))"
						: `${collapsedSideTitlebarWidth}px`,
				}}
			>
				{sidePanelCollapsed ? (
					<button
						type="button"
						data-window-panel-toggle="left"
						className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim transition-colors hover:!bg-bg-hover hover:text-fg"
						onClick={toggleSidePanel}
						title="Show Side Panel"
						aria-label="Show Side Panel"
					>
						<PanelLeftOpen size={13} />
					</button>
				) : (
					<div
						data-titlebar-workspace-tabs
						className="min-w-0 flex-1 self-stretch"
					>
						{workspaceTabs}
					</div>
				)}
			</div>
		);
	};
	const sidePanelElement = (
		<Panel
			id="side-panel"
			panelRef={sidePanelRef}
			defaultSize={`${SIDE_PANEL_DEFAULT_SIZE}px`}
			minSize={`${SIDE_PANEL_MIN_SIZE}px`}
			maxSize={`${SIDE_PANEL_MAX_SIZE}px`}
			collapsedSize="0px"
			collapsible
			groupResizeBehavior="preserve-pixel-size"
			onResize={mobile ? undefined : handleSidePanelResize}
			data-layout-panel="side"
			data-panel-side="left"
			inert={sidePanelCollapsed}
			className="h-full overflow-hidden"
		>
			<div
				data-panel-content="side"
				className={`mr-auto h-full overflow-hidden transition-[transform,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)] ${
					sidePanelCollapsed
						? "pointer-events-none -translate-x-full opacity-0"
						: "translate-x-0 opacity-100"
				}`}
				style={{ width: "var(--side-panel-width)" }}
			>
				{sidePanelContent}
			</div>
		</Panel>
	);
	const sidePanelResizeHandle = (
		<ResizeHandle label="Resize Side Panel" hidden={sidePanelCollapsed} />
	);
	const terminalDockVisible = terminalDocked && !!activeDockedTerminal;
	const visibleTerminalId = terminalDockVisible
		? activeDockedTerminal?.id
		: activeTab.type === "terminal"
			? activeTab.id
			: undefined;
	const terminalSurfaceStyle = (tab: CenterTab): CSSProperties => {
		if (terminalDockVisible)
			return {
				left: 0,
				right: RIGHT_PANEL_WIDTH,
				bottom: 0,
				height: "var(--terminal-panel-height)",
			};
		if (!rightTab) return { inset: 0 };
		return paneOf(tab) === "right"
			? {
					top: 0,
					right: 0,
					bottom: 0,
					left: "calc(100% - var(--right-pane-width))",
				}
			: { top: 0, right: "var(--right-pane-width)", bottom: 0, left: 0 };
	};
	const centerMain = (
		<main className="flex h-full min-h-0 w-full flex-1 flex-col overflow-hidden bg-bg">
			<div className="min-h-0 flex-1 overflow-hidden">
				{renderCenterContent()}
			</div>
		</main>
	);
	const centerPanelElement = (
		<Panel
			id="center"
			minSize={`${CENTER_PANEL_MIN_SIZE}px`}
			data-layout-panel="center"
			className="relative flex h-full min-w-0 flex-col overflow-hidden bg-bg"
		>
			<Group
				id="center-terminal-layout"
				orientation="vertical"
				className="h-full min-h-0 overflow-hidden"
			>
				<Panel
					id="center-content"
					minSize="200px"
					className="flex min-h-0 flex-col overflow-hidden"
				>
					{centerMain}
				</Panel>
				{terminalDockVisible && (
					<ResizeHandle
						label="Resize Terminal Dock"
						hidden={false}
						orientation="vertical"
					/>
				)}
				{terminalDockVisible && (
					<Panel
						id="terminal-dock"
						defaultSize={`${TERMINAL_PANEL_DEFAULT_SIZE}px`}
						minSize={`${TERMINAL_PANEL_MIN_SIZE}px`}
						maxSize="60%"
						onResize={handleTerminalDockResize}
						data-layout-panel="terminal"
						className="min-h-0 overflow-hidden border-t border-border-subtle"
					>
						<div className="flex h-full min-h-0" aria-label="Terminal dock">
							<div
								className="min-w-0 flex-1 overflow-hidden"
								role="tabpanel"
								aria-label={activeDockedTerminal.label}
							/>
							<div
								data-terminal-tabs-separator
								aria-hidden="true"
								className="w-px shrink-0 bg-border"
							/>
							<TerminalDockTabs
								tabs={terminalTabs}
								activeTabId={activeDockedTerminal.id}
								onActivate={setActiveDockedTerminalId}
								onClose={(id) => void requestCloseTab(id)}
								onCloseMany={(ids) => void closeTabs(ids)}
								onNew={() => void createTerminal()}
							/>
						</div>
					</Panel>
				)}
			</Group>
			{terminalTabs.map((tab) => {
				const visible = tab.id === visibleTerminalId;
				return (
					<div
						key={tab.id}
						data-terminal-surface={tab.id}
						className={visible ? "absolute z-10 overflow-hidden" : "hidden"}
						style={visible ? terminalSurfaceStyle(tab) : undefined}
					>
						<ErrorBoundary
							fallback={(error, _reset, errorInfo) => (
								<TabCrashed error={error} errorInfo={errorInfo} />
							)}
						>
							<TerminalView
								id={tab.terminalId!}
								active={visible}
								onExit={() => closeTabNow(tab.id)}
								onTitle={setTerminalTitle}
							/>
						</ErrorBoundary>
					</div>
				);
			})}
		</Panel>
	);

	return (
		<div
			ref={appRef}
			data-mobile-layout={mobile || undefined}
			className="relative flex h-dvh flex-col bg-bg text-fg"
			style={
				{
					"--side-panel-width": `${SIDE_PANEL_DEFAULT_SIZE}px`,
					"--right-pane-width": "0px",
					"--terminal-panel-height": `${TERMINAL_PANEL_DEFAULT_SIZE}px`,
				} as CSSProperties
			}
		>
			{mobile && (
				<MobileNavigation
					title={
						activeTab.type === "chat"
							? chatTabLabel(activeTab)
							: activeTab.label
					}
					connected={connected}
					running={phase !== "idle"}
					currentSessionId={sessionId}
					runningSessionIds={runningSessionIds}
					onSelectBackend={handleBackendSelect}
					onSelectSession={(id) => void handleSessionSelect(id, "keep")}
					onDeleteSession={(id, title) => setSessionDelete({ id, title })}
					onNewSession={() => void handleNewSession()}
					onBackToChat={activeTab.type === "chat" ? undefined : focusChat}
				/>
			)}
			<div
				data-panel-frame="side"
				data-panel-side="left"
				aria-hidden="true"
				className={`pointer-events-none absolute inset-y-0 left-0 z-0 rounded-[10px] bg-bg-surface/40 transition-[transform,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)] ${
					sidePanelCollapsed
						? "-translate-x-full opacity-0"
						: "translate-x-0 opacity-100"
				}`}
				style={{ width: "var(--side-panel-width)" }}
			/>
			<header
				data-window-titlebar
				className="window-titlebar relative z-10 flex shrink-0 items-stretch overflow-hidden bg-transparent"
				aria-label="Window toolbar"
			>
				<div
					data-titlebar-separator
					aria-hidden="true"
					className="pointer-events-none absolute bottom-0 h-px bg-border-subtle"
					style={{
						left: sidePanelDocked ? "var(--side-panel-width)" : "0px",
						right: "0px",
					}}
				/>
				<div
					className="window-titlebar-controls-spacer shrink-0"
					aria-hidden="true"
				/>
				<AppMenu
					canCreateFile={!workspaceSwitching}
					canOpenFolder={!workspaceSwitching}
					canSave={canSaveFile}
				/>
				{renderSidePanelTitlebar()}

				<TabStrip
					items={leftStripItems}
					activeTabId={leftTab?.id ?? ""}
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
						tabCount={centerTabs.length}
						preview={!!tabs.find((tab) => tab.id === tabMenu.tabId)?.preview}
						pane={paneOf(
							tabs.find((tab) => tab.id === tabMenu.tabId) ??
								leftTab ??
								EMPTY_CENTER_TAB,
						)}
						canMoveRight={leftTabs.length > 1}
						onMove={moveTabToPane}
						onClose={() => setTabMenu(null)}
						onKeepOpen={keepTab}
						onCloseTab={(id) => void closeTabs([id])}
						onCloseOthers={(id) =>
							void closeTabs(
								centerTabs.filter((tab) => tab.id !== id).map((tab) => tab.id),
							)
						}
						onCloseAll={() => void closeTabs(centerTabs.map((tab) => tab.id))}
					/>
				)}

				{activeTab.pane !== "right" && titlebarActions}
				{rightTab && (
					<div
						className="flex shrink-0 items-stretch overflow-hidden"
						style={{
							width: "var(--right-pane-width)",
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
					className="window-titlebar-controls-spacer-end shrink-0"
					aria-hidden="true"
				/>
			</header>
			<Group
				id="wingman-layout"
				orientation="horizontal"
				className="relative z-10 flex-1 overflow-hidden"
			>
				{sidePanelElement}
				{sidePanelResizeHandle}
				{centerPanelElement}
			</Group>

			{paletteOpen && (
				<CommandPalette
					settings={activeSettings}
					setSettings={setActiveSettings}
					sessionId={sessionId}
					onClose={() => {
						setPaletteOpen(false);
						setPaletteEditorActions(null);
					}}
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
							if (
								session?.state === "terminated" &&
								session.error &&
								session.session_id !== previousID
							) {
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
				title="Save As"
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
							autoCapitalize="none"
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
							onFocus={(event) => event.currentTarget.select()}
							disabled={filePathRequest?.submitting}
							autoComplete="off"
							autoCorrect="off"
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
						{filePathRequest?.submitting ? "Saving..." : "Save"}
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

function EmptyWorkspace() {
	return (
		<div
			data-empty-workspace
			className="flex h-full items-center justify-center bg-bg"
		>
			<picture className="opacity-20">
				<source
					media="(prefers-color-scheme: light)"
					srcSet="/icon_light.svg"
				/>
				<img
					src="/icon_dark.svg"
					alt=""
					aria-hidden="true"
					className="h-16 w-16"
				/>
			</picture>
		</div>
	);
}

function ChatTabLayout({
	children,
	view,
	sessionId,
	backendId,
	showAgents,
	runningSessionIds,
	onSessionSelect,
	onSessionDelete,
	onOpenTask,
}: {
	children: ReactNode;
	view?: ChatAuxiliaryView;
	sessionId: string;
	backendId: string;
	showAgents: boolean;
	runningSessionIds: Set<string>;
	onSessionSelect: (id: string, disposition?: TabDisposition) => void;
	onSessionDelete: (id: string, title: string) => void;
	onOpenTask: (task: TaskEntry) => void;
}) {
	return (
		<div className="flex h-full min-h-0 min-w-0 overflow-hidden">
			<div className="min-h-0 min-w-0 flex-1">{children}</div>
			<aside
				data-chat-auxiliary-panel
				data-view={view ?? "closed"}
				aria-label={
					view === "sessions"
						? "Sessions"
						: view === "agents"
							? "Background tasks"
							: "Chat details"
				}
				inert={!view}
				className={`h-full shrink-0 overflow-hidden transition-[width] duration-150 ${view ? "border-l border-border-subtle" : ""}`}
				style={{ width: view ? RIGHT_PANEL_WIDTH : "0px" }}
			>
				<div className="h-full w-full bg-bg">
					<div className={view === "sessions" ? "h-full" : "hidden"}>
						<AgentSessions
							backendId={backendId}
							currentSessionId={sessionId}
							runningSessionIds={runningSessionIds}
							onSessionSelect={onSessionSelect}
							onSessionDelete={onSessionDelete}
						/>
					</div>
					{showAgents && (
						<div className={view === "agents" ? "h-full" : "hidden"}>
							<TasksPanel
								sessionId={isDraft(sessionId) ? "" : sessionId}
								onOpenTask={onOpenTask}
							/>
						</div>
					)}
				</div>
			</aside>
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

function TerminalDockTabs({
	tabs,
	activeTabId,
	onActivate,
	onClose,
	onCloseMany,
	onNew,
}: {
	tabs: CenterTab[];
	activeTabId: string;
	onActivate: (id: string) => void;
	onClose: (id: string) => void;
	onCloseMany: (ids: string[]) => void;
	onNew: () => void;
}) {
	const [menu, setMenu] = useState<{ x: number; y: number; tabId: string }>();
	const openMenu = (tabId: string, x: number, y: number) => {
		onActivate(tabId);
		setMenu({ x, y, tabId });
	};

	return (
		<aside
			className="flex shrink-0 flex-col bg-bg"
			style={{ width: `calc(${RIGHT_PANEL_WIDTH} - 1px)` }}
		>
			<div className="flex h-8 shrink-0 items-center gap-1 px-2">
				<span className="min-w-0 flex-1 truncate text-[10px] font-medium uppercase tracking-wide text-fg-dim">
					Terminals
				</span>
				<button
					type="button"
					className="flex h-6 w-6 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
					onClick={onNew}
					title="New terminal"
					aria-label="New terminal"
				>
					<Plus size={11} />
				</button>
			</div>
			<div
				className="min-h-0 flex-1 overflow-y-auto py-1"
				role="tablist"
				aria-label="Terminal tabs"
				aria-orientation="vertical"
			>
				{tabs.map((tab, index) => {
					const active = tab.id === activeTabId;
					return (
						<button
							key={tab.id}
							type="button"
							role="tab"
							aria-selected={active}
							tabIndex={active ? 0 : -1}
							className={`flex w-full min-w-0 items-center gap-2 border-l-2 px-2 py-1.5 text-left text-[11px] transition-colors ${
								active
									? "border-accent bg-bg-active text-fg"
									: "border-transparent text-fg-dim hover:bg-bg-hover hover:text-fg-muted"
							}`}
							onClick={() => onActivate(tab.id)}
							onContextMenu={(event) => {
								event.preventDefault();
								openMenu(tab.id, event.clientX, event.clientY);
							}}
							onKeyDown={(event) => {
								if (event.key === "Delete") {
									event.preventDefault();
									onClose(tab.id);
									return;
								}
								if (
									event.key === "ContextMenu" ||
									(event.shiftKey && event.key === "F10")
								) {
									event.preventDefault();
									const bounds = event.currentTarget.getBoundingClientRect();
									openMenu(tab.id, bounds.left + 12, bounds.bottom);
									return;
								}
								if (event.key !== "ArrowUp" && event.key !== "ArrowDown")
									return;
								const offset = event.key === "ArrowUp" ? -1 : 1;
								const next = tabs[(index + offset + tabs.length) % tabs.length];
								event.preventDefault();
								if (!next) return;
								const tablist = event.currentTarget.parentElement;
								onActivate(next.id);
								requestAnimationFrame(() => {
									tablist
										?.querySelector<HTMLButtonElement>(
											`[role="tab"][aria-selected="true"]`,
										)
										?.focus();
								});
							}}
							title={tab.label}
						>
							<SquareTerminal size={12} className="shrink-0" />
							<span className="truncate">{tab.label}</span>
						</button>
					);
				})}
			</div>
			{menu && (
				<TerminalTabContextMenu
					x={menu.x}
					y={menu.y}
					tabId={menu.tabId}
					tabs={tabs}
					onClose={() => setMenu(undefined)}
					onCloseTab={onClose}
					onCloseMany={onCloseMany}
				/>
			)}
		</aside>
	);
}

function TerminalTabContextMenu({
	x,
	y,
	tabId,
	tabs,
	onClose,
	onCloseTab,
	onCloseMany,
}: {
	x: number;
	y: number;
	tabId: string;
	tabs: CenterTab[];
	onClose: () => void;
	onCloseTab: (id: string) => void;
	onCloseMany: (ids: string[]) => void;
}) {
	const actions = [
		{ label: "Close", disabled: false, run: () => onCloseTab(tabId) },
		{
			label: "Close Others",
			disabled: tabs.length < 2,
			run: () =>
				onCloseMany(
					tabs.filter((tab) => tab.id !== tabId).map((tab) => tab.id),
				),
		},
		{
			label: "Close All",
			disabled: tabs.length === 0,
			run: () => onCloseMany(tabs.map((tab) => tab.id)),
		},
	];
	return (
		<FloatingMenu
			open
			onOpenChange={(open) => !open && onClose()}
			reference={{ x, y }}
			label="Terminal actions"
			className="z-[140] min-w-[150px] rounded-md border border-border bg-bg-elevated/95 py-1 shadow-xl backdrop-blur-sm"
		>
			{actions.map((action) => (
				<button
					key={action.label}
					type="button"
					role="menuitem"
					disabled={action.disabled}
					onClick={() => {
						onClose();
						action.run();
					}}
					className="flex w-full px-3 py-1.5 text-left text-[11.5px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg disabled:cursor-default disabled:opacity-40"
				>
					{action.label}
				</button>
			))}
		</FloatingMenu>
	);
}

function ResizeHandle({
	label,
	hidden,
	orientation = "horizontal",
}: {
	label: string;
	hidden: boolean;
	orientation?: "horizontal" | "vertical";
}) {
	return (
		<Separator
			aria-label={label}
			disabled={hidden}
			className={`relative z-20 shrink-0 bg-transparent outline-none ${
				orientation === "vertical"
					? "-my-1.5 h-3 w-full cursor-row-resize"
					: "-mx-1.5 w-3 cursor-col-resize"
			} ${hidden ? "pointer-events-none" : ""}`}
		>
			{orientation === "vertical" && (
				<span
					data-terminal-dock-separator
					aria-hidden="true"
					className="pointer-events-none absolute inset-x-0 top-1/2 h-px bg-border-strong"
				/>
			)}
		</Separator>
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

function AppMenu({
	canCreateFile,
	canOpenFolder,
	canSave,
}: {
	canCreateFile: boolean;
	canOpenFolder: boolean;
	canSave: boolean;
}) {
	const [open, setOpen] = useState(false);
	const [button, setButton] = useState<HTMLButtonElement | null>(null);

	const run = (command: string) => {
		setOpen(false);
		window.dispatchEvent(new CustomEvent("shell:command", { detail: command }));
	};

	return (
		<div className="window-app-menu relative shrink-0 items-center justify-center">
			<button
				ref={setButton}
				type="button"
				className="flex h-7 w-7 items-center justify-center text-fg-dim transition-colors hover:text-fg"
				title="File menu"
				aria-label="File menu"
				aria-haspopup="menu"
				aria-expanded={open}
				onClick={() => setOpen((value) => !value)}
			>
				<MenuIcon size={14} />
			</button>
			<FloatingMenu
				open={open}
				onOpenChange={setOpen}
				reference={button}
				placement="bottom-start"
				gap={4}
				label="File"
				className="z-[140] min-w-[190px] rounded-md border border-border-subtle bg-bg-elevated py-1 text-[12px] shadow-2xl"
			>
				<AppMenuItem
					icon={<FilePlus size={12} />}
					label="New Text File"
					shortcut="Ctrl+N"
					disabled={!canCreateFile}
					onClick={() => run("new-file")}
				/>
				<AppMenuItem
					icon={<FolderOpen size={12} />}
					label="Open Folder…"
					shortcut="Ctrl+O"
					disabled={!canOpenFolder}
					onClick={() => run("open-folder")}
				/>
				<div role="separator" className="my-1 border-t border-border-subtle" />
				<AppMenuItem
					icon={<Save size={12} />}
					label="Save"
					shortcut="Ctrl+S"
					disabled={!canSave}
					onClick={() => run("save")}
				/>
				<AppMenuItem
					icon={<SaveAll size={12} />}
					label="Save As…"
					shortcut="Ctrl+Shift+S"
					disabled={!canSave}
					onClick={() => run("save-as")}
				/>
			</FloatingMenu>
		</div>
	);
}

function AppMenuItem({
	icon,
	label,
	shortcut,
	disabled,
	onClick,
}: {
	icon: ReactNode;
	label: string;
	shortcut: string;
	disabled?: boolean;
	onClick: () => void;
}) {
	return (
		<button
			type="button"
			role="menuitem"
			disabled={disabled}
			className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-fg-muted transition-colors enabled:hover:bg-bg-hover enabled:hover:text-fg disabled:cursor-default disabled:opacity-40"
			onClick={onClick}
		>
			<span className="flex w-3.5 shrink-0 items-center justify-center text-fg-dim">
				{icon}
			</span>
			<span className="min-w-0 flex-1">{label}</span>
			<span className="ml-4 text-[10px] text-fg-dim">{shortcut}</span>
		</button>
	);
}

function WorkspaceTabButton({
	active,
	icon: Icon,
	label,
	iconOnly,
	onClick,
}: {
	active: boolean;
	icon: LucideIcon;
	label: string;
	iconOnly: boolean;
	onClick: () => void;
}) {
	return (
		<button
			type="button"
			role="tab"
			aria-selected={active}
			aria-label={label}
			tabIndex={active ? 0 : -1}
			title={iconOnly ? label : undefined}
			className={`relative flex min-w-0 flex-1 cursor-pointer items-center justify-center overflow-hidden px-1.5 text-[11px] font-medium transition-colors ${
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
			{iconOnly ? (
				<Icon size={13} className="shrink-0" aria-hidden="true" />
			) : (
				<span className="truncate">{label}</span>
			)}
			{active && (
				<span className="absolute right-2 bottom-0 left-2 h-[2px] rounded-full bg-accent" />
			)}
		</button>
	);
}

function NewItemLauncher({
	onNewChat,
	onNewTerminal,
	onNewTextFile,
}: {
	onNewChat: () => void;
	onNewTerminal?: () => void;
	onNewTextFile?: () => void;
}) {
	const [open, setOpen] = useState(false);
	const [launcher, setLauncher] = useState<HTMLDivElement | null>(null);
	const run = (action: () => void) => {
		setOpen(false);
		action();
	};

	return (
		<div
			ref={setLauncher}
			className="relative self-center flex h-8 w-8 shrink-0"
		>
			<button
				type="button"
				className="flex h-8 w-8 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
				onClick={() => setOpen((value) => !value)}
				title="New…"
				aria-label="New"
				aria-haspopup="menu"
				aria-expanded={open}
			>
				<Plus size={14} />
			</button>
			<FloatingMenu
				open={open}
				onOpenChange={setOpen}
				reference={launcher}
				placement="bottom-end"
				label="New"
				maxHeight={220}
				className="z-[100] min-w-[160px] overflow-y-auto rounded-md border border-border bg-bg-elevated/95 py-1 text-[11.5px] shadow-xl backdrop-blur-sm"
			>
				<NewItemMenuButton
					icon={<MessageSquarePlus size={12} />}
					label="Chat"
					onClick={() => run(onNewChat)}
				/>
				{onNewTerminal && (
					<NewItemMenuButton
						icon={<SquareTerminal size={12} />}
						label="Terminal"
						onClick={() => run(onNewTerminal)}
					/>
				)}
				{onNewTextFile && (
					<NewItemMenuButton
						icon={<FilePlus size={12} />}
						label="Text File"
						onClick={() => run(onNewTextFile)}
					/>
				)}
			</FloatingMenu>
		</div>
	);
}

function NewItemMenuButton({
	icon,
	label,
	onClick,
}: {
	icon: ReactNode;
	label: string;
	onClick: () => void;
}) {
	return (
		<button
			type="button"
			role="menuitem"
			onClick={onClick}
			className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
		>
			<span className="text-fg-dim">{icon}</span>
			<span>{label}</span>
		</button>
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
	const [button, setButton] = useState<HTMLButtonElement | null>(null);
	const hasContext = contextWindow > 0 && lastInputTokens > 0;
	const used = hasContext ? Math.min(lastInputTokens, contextWindow) : 0;
	const usedPercent = hasContext ? Math.round((used / contextWindow) * 100) : 0;
	const leftPercent =
		contextRemainingPercent(lastInputTokens, contextWindow) ?? 0;
	const freeTokens = hasContext ? Math.max(0, contextWindow - used) : 0;
	const visible = shouldShowContextIndicator(lastInputTokens, contextWindow);
	if (!visible && open) setOpen(false);
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
	if (!visible) return null;

	return (
		<>
			<button
				ref={setButton}
				type="button"
				className={`flex h-8 shrink-0 items-center rounded-md px-2 text-[11px] tabular-nums transition-colors hover:bg-bg-hover ${tone}`}
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
				reference={button}
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
