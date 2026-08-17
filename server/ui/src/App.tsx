import {
	ChevronDown,
	Compass,
	Code2,
	Eye,
	FileText,
	GitCompare,
	Globe2,
	Lightbulb,
	Loader2,
	PanelLeftOpen,
	PanelRightOpen,
	Plus,
	Save,
	Search,
	SquareTerminal,
	Wrench,
} from "lucide-react";
import {
	type CSSProperties,
	type ErrorInfo,
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
import { createWorkspaceFile } from "./api/files";
import { ChatPanel } from "./components/ChatPanel";
import { Tab } from "./components/Tab";
import {
	CommandPalette,
	type PaletteAction,
	type PaletteSkill,
} from "./components/CommandPalette";
import type { ModeOption } from "./components/ModePicker";
import { DiffsPanel } from "./components/DiffsPanel";
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
import { Dialog, dialogButtonClass, useToast } from "./components/ui/Feedback";
import { FloatingMenu, FloatingSurface } from "./components/ui/Floating";
import { AgentPicker, BUILTIN_AGENT_ID } from "./components/AgentPicker";
import { Sidebar } from "./components/Sidebar";
import { useCapabilities } from "./hooks/useCapabilities";
import { useOpenDocuments } from "./hooks/useOpenDocuments";
import {
	type ChatEntry,
	type PromptReply,
	useWebSocket,
} from "./hooks/useWebSocket";
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

interface CenterTab {
	id: string;
	type: "chat" | "file" | "diff" | "compare" | "terminal" | "task" | "graph";
	label: string;
	path?: string;
	diffLayer?: DiffLayer;
	compareBase?: string;
	compareHead?: string;
	compareMode?: CompareMode;
	line?: number;
	column?: number;
	navigationKey?: number;
	external?: boolean;
	sessionId?: string;
	terminalId?: string;
	taskId?: string;
	preview?: boolean;
}

type RightTab = "changes" | "files" | "problems" | "agents";
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

const EMPTY_ENTRIES: never[] = [];
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

const chatTabId = (sessionId: string) => `chat:${sessionId}`;

function draftChatTab(): CenterTab {
	return {
		id: chatTabId(""),
		type: "chat",
		label: "Agent",
		sessionId: "",
	};
}

function withSessionFallback(tabs: CenterTab[]): CenterTab[] {
	return tabs.some((tab) => tab.type === "chat")
		? tabs
		: [draftChatTab(), ...tabs];
}

function placeCenterTab(
	current: CenterTab[],
	candidate: CenterTab,
	disposition: TabDisposition,
	dirtyPaths: ReadonlySet<string>,
): { tabs: CenterTab[]; replaced?: CenterTab } {
	const existingIndex = current.findIndex((tab) => tab.id === candidate.id);
	if (existingIndex >= 0) {
		const existing = current[existingIndex];
		if (disposition !== "keep" || !existing.preview) return { tabs: current };
		const tabs = [...current];
		tabs[existingIndex] = { ...existing, preview: undefined };
		return { tabs };
	}

	const placed: CenterTab = {
		...candidate,
		preview: disposition === "preview" || undefined,
	};
	if (disposition === "keep") return { tabs: [...current, placed] };

	const previewIndex = current.findIndex((tab) => tab.preview);
	if (previewIndex < 0) return { tabs: [...current, placed] };
	const previous = current[previewIndex];
	if (
		previous.type === "file" &&
		previous.path &&
		dirtyPaths.has(previous.path)
	) {
		const tabs = current.map((tab, index) =>
			index === previewIndex ? { ...tab, preview: undefined } : tab,
		);
		return { tabs: [...tabs, placed] };
	}
	const tabs = [...current];
	tabs[previewIndex] = placed;
	return { tabs, replaced: previous };
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
	const toast = useToast();
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
	const capabilities = useCapabilities(subscribe);
	const showChanges = !!(capabilities?.diffs || capabilities?.git_init);
	const showProblems = capabilities?.lsp ?? false;
	const showAgents = capabilities?.tasks ?? false;
	const showTerminal = capabilities?.terminal ?? false;
	const tabEnabled = capabilities?.tab ?? false;
	const [requestedRightTab, setRequestedRightTab] = useState<RightTab>("files");
	const [workspaceSearching, setWorkspaceSearching] = useState(false);
	const [searchFocusKey, setSearchFocusKey] = useState(0);
	const [sidebarCollapsed, setSidebarCollapsed] = useState(true);
	const [rightPanelCollapsed, setRightPanelCollapsed] = useState(false);
	const appRef = useRef<HTMLDivElement>(null);
	const leftPanelWidthRef = useRef(LEFT_PANEL_DEFAULT_SIZE);
	const rightPanelDefaultWidth = RIGHT_PANEL_DEFAULT_SIZE;
	const rightPanelWidthRef = useRef(rightPanelDefaultWidth);
	const leftPanelRef = usePanelRef();
	const rightPanelRef = usePanelRef();
	const [terminalShells, setTerminalShells] = useState<ShellEntry[]>([]);
	const terminalCreatingRef = useRef(false);

	const [tabs, setTabs] = useState<CenterTab[]>([draftChatTab()]);
	const [activeTabId, setActiveTabId] = useState(chatTabId(""));
	const tabListRef = useRef<HTMLDivElement>(null);
	useEffect(() => {
		const frame = requestAnimationFrame(() => {
			const list = tabListRef.current;
			const tab = list?.querySelector<HTMLElement>(
				`[data-center-tab="${CSS.escape(activeTabId)}"]`,
			);
			if (!list || !tab) return;
			const listRect = list.getBoundingClientRect();
			const tabRect = tab.getBoundingClientRect();
			if (tabRect.left < listRect.left) {
				list.scrollLeft -= listRect.left - tabRect.left;
			} else if (tabRect.right > listRect.right) {
				list.scrollLeft += tabRect.right - listRect.right;
			}
		});
		return () => cancelAnimationFrame(frame);
	}, [activeTabId, tabs.length]);
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
				const response = await fetch("/api/terminals", {
					method: "POST",
					headers: { "Content-Type": "application/json" },
					body: JSON.stringify({ shell, cols: 80, rows: 24 }),
				});
				if (!response.ok) {
					throw new Error(
						(await response.text()).trim() ||
							`Failed to create terminal (${response.status}).`,
					);
				}
				const entry = (await response.json()) as TerminalEntry;
				const tab: CenterTab = {
					id: `terminal:${entry.id}`,
					type: "terminal",
					label: entry.title,
					terminalId: entry.id,
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
		[toast],
	);

	useEffect(() => {
		if (!showTerminal) return;
		let cancelled = false;
		fetch("/api/terminals/shells")
			.then((response) => (response.ok ? response.json() : []))
			.then((shells: ShellEntry[]) => {
				if (!cancelled) setTerminalShells(shells);
			})
			.catch(() => {
				if (!cancelled) setTerminalShells([]);
			});
		return () => {
			cancelled = true;
		};
	}, [showTerminal]);

	const reloadTerminals = useCallback(async () => {
		let entries: TerminalEntry[];
		try {
			const response = await fetch("/api/terminals");
			if (!response.ok) return;
			entries = (await response.json()) as TerminalEntry[];
		} catch {
			return;
		}
		const ids = new Set(entries.map((entry) => entry.id));
		setTabs((prev) => {
			const next = prev.filter(
				(tab) => tab.type !== "terminal" || ids.has(tab.terminalId ?? ""),
			);
			const known = new Set(
				next
					.filter((tab) => tab.type === "terminal")
					.map((tab) => tab.terminalId),
			);
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
	}, []);

	useEffect(() => {
		if (!showTerminal) return;
		void reloadTerminals();
		return subscribe((msg) => {
			if (msg.type === "terminals_changed") void reloadTerminals();
		});
	}, [showTerminal, subscribe, reloadTerminals]);

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
	const rightTab =
		(requestedRightTab === "changes" && !showChanges) ||
		(requestedRightTab === "problems" && !showProblems) ||
		(requestedRightTab === "agents" && !showAgents)
			? "files"
			: requestedRightTab;

	const activeSession = sessionId ? sessions[sessionId] : undefined;
	const entries = activeSession?.entries ?? EMPTY_ENTRIES;
	const sessionError = activeSession?.error ?? null;
	const phase = activeSession?.phase ?? "idle";
	const usage = activeSession?.usage ?? EMPTY_USAGE;
	const prompt = activeSession?.prompt ?? null;
	const pendingInputs = activeSession?.pendingInputs ?? EMPTY_ENTRIES;
	const queuePaused = activeSession?.queuePaused ?? false;
	const canSteer = activeSession?.canSteer ?? false;

	const [agentId, setAgentId] = useState("");
	const [switchingAgent, setSwitchingAgent] = useState<string | null>(null);
	const loadAgent = useCallback(async (): Promise<string> => {
		try {
			const r = await fetch("/api/agent");
			const data = (await r.json()) as { agent?: string };
			const id = data.agent || BUILTIN_AGENT_ID;
			setAgentId(id);
			return id;
		} catch {
			return "";
		}
	}, []);

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
				candidate,
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
		[activateTab, closeDocument, dirtyPaths, tabs],
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

	const handlePromptReply = useCallback(
		(reply: PromptReply) => {
			if (sessionId && prompt) {
				respondPrompt(sessionId, prompt.id, reply);
			}
		},
		[respondPrompt, sessionId, prompt],
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
				setActiveTabId(existing.id);
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
		[keepTab, openDocument, showCenterTab, tabs],
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
				setActiveTabId(existing.id);
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
		[keepTab, showCenterTab, tabs],
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
				const fallback = remaining.some((t) => t.type === "chat")
					? (remaining[Math.min(idx, remaining.length - 1)] ?? draftChatTab())
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
				const response = await fetch(
					`/api/terminals/${encodeURIComponent(tab.terminalId)}`,
					{ method: "DELETE" },
				);
				if (!response.ok && response.status !== 404) {
					throw new Error(
						(await response.text()).trim() ||
							`Failed to close terminal (${response.status}).`,
					);
				}
				setCloseRequest((current) =>
					current?.kind === "terminal" && current.tab.id === tab.id
						? null
						: current,
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
		[closeTabNow, toast],
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
			const selected = await fetch("/app/folder", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: "{}",
			});
			if (!selected.ok) {
				throw new Error(
					(await selected.text()).trim() || "Could not select a folder.",
				);
			}
			const { path } = (await selected.json()) as { path?: string };
			if (!path) {
				setWorkspaceSwitching(false);
				return;
			}

			const opened = await fetch("/app/workspaces/open", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ path, replace: true }),
			});
			if (!opened.ok) {
				throw new Error(
					(await opened.text()).trim() || "Could not open the folder.",
				);
			}
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
			if (tab.type === "file" && tab.path && dirtyPaths.has(tab.path)) {
				setCloseRequest({ kind: "file", tab });
				return;
			}
			if (tab.type === "terminal" && tab.terminalId) {
				try {
					for (let attempt = 0; attempt < 2; attempt++) {
						const response = await fetch(
							`/api/terminals/${encodeURIComponent(tab.terminalId)}`,
						);
						if (response.status === 404) {
							closeTabNow(tab.id);
							return;
						}
						if (!response.ok) {
							throw new Error(
								(await response.text()).trim() ||
									`Failed to inspect terminal (${response.status}).`,
							);
						}
						const terminal = (await response.json()) as TerminalEntry;
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
		[closeTabNow, closeTerminal, dirtyPaths, tabs, toast],
	);

	const [tabMenu, setTabMenu] = useState<{
		x: number;
		y: number;
		tabId?: string;
	} | null>(null);

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
			const res = await fetch("/api/sessions", { method: "POST" });
			if (!res.ok) throw new Error(await res.text());
			const data = (await res.json()) as { id?: string };
			if (!data.id) return;
			openChatTab(data.id, "keep", true);
		} catch (error) {
			toast({
				title: "Could not create session",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		}
	}, [openChatTab, toast]);

	const handleSessionDeleted = useCallback(
		(id: string) => {
			removeSession(id);
			setCurrentSessionId((prev) => (prev === id ? "" : prev));
			const tab = tabs.find((t) => t.type === "chat" && t.sessionId === id);
			if (tab) closeTabNow(tab.id);
		},
		[removeSession, tabs, closeTabNow],
	);

	const deleteSession = useCallback(async () => {
		if (!sessionDelete) return;
		try {
			const response = await fetch(`/api/sessions/${sessionDelete.id}`, {
				method: "DELETE",
			});
			if (!response.ok) {
				throw new Error(
					(await response.text()).trim() ||
						`Failed to delete session (${response.status}).`,
				);
			}
			const id = sessionDelete.id;
			setSessionDelete(null);
			handleSessionDeleted(id);
		} catch (error) {
			toast({
				title: "Session was not deleted",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		}
	}, [handleSessionDeleted, sessionDelete, toast]);

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
						const res = await fetch(`/api/sessions/${id}/load`, {
							method: "POST",
						});
						if (res.ok) return null;
						return (
							(await res.text()).trim() ||
							`Failed to load session (${res.status}).`
						);
					} catch {
						return "Failed to load session.";
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
			void loadAgent();
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
	}, [
		subscribe,
		clearSessions,
		loadAgent,
		handleSessionSelect,
		handleNewSession,
	]);

	useEffect(() => {
		const [agent, sid] = window.location.pathname
			.split("/")
			.filter(Boolean)
			.map(decodeURIComponent);
		(async () => {
			deepLinkRef.current = sid ?? null;
			const current = await loadAgent();
			if (!current) {
				deepLinkRef.current = null;
				return;
			}
			if (!agent || agent === current) {
				if (sid) void handleSessionSelect(sid);
				deepLinkRef.current = null;
				return;
			}
			try {
				const res = await fetch("/api/agent", {
					method: "POST",
					headers: { "Content-Type": "application/json" },
					body: JSON.stringify({ agent }),
				});
				if (!res.ok) {
					deepLinkRef.current = null;
					return;
				}
				setAgentId(agent);
			} catch {
				deepLinkRef.current = null;
			}
		})();
		// eslint-disable-next-line react-hooks/exhaustive-deps -- initial URL only
	}, []);

	const ensureSessionId = useCallback(async (): Promise<string> => {
		if (sessionId) return sessionId;
		const res = await fetch("/api/sessions", { method: "POST" });
		if (!res.ok) throw new Error("failed to allocate session");
		const data = (await res.json()) as { id?: string };
		if (!data.id) throw new Error("session id missing in response");
		openChatTab(data.id, "keep", true);
		return data.id;
	}, [sessionId, openChatTab]);

	const handleSend = useCallback(
		async (
			text: string,
			files?: string[],
			images?: string[],
			intent: TurnInputIntent = "follow_up",
		): Promise<boolean> => {
			try {
				const sid = await ensureSessionId();
				return sendChat(sid, text, files, images, intent);
			} catch {
				return false;
			}
		},
		[sendChat, ensureSessionId],
	);

	const startInsightsAnalysis = useCallback(
		async (command: string) => {
			try {
				const response = await fetch("/api/sessions", { method: "POST" });
				if (!response.ok) throw new Error(await response.text());
				const data = (await response.json()) as { id?: string };
				if (!data.id) throw new Error("Session id missing in response");
				openChatTab(data.id, "keep");
				if (!sendChat(data.id, command)) {
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
		[openChatTab, sendChat, toast],
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

	const [modes, setModes] = useState<ModeOption[]>([]);
	const [mode, setMode] = useState<string>("");

	useEffect(() => {
		const url = sessionId
			? `/api/sessions/${encodeURIComponent(sessionId)}/mode`
			: "/api/mode";
		fetch(url)
			.then((r) => r.json())
			.then((data) => {
				setModes(data.modes ?? []);
				setMode(data.current ?? "");
			})
			.catch(() => {});
	}, [sessionId]);

	const selectMode = useCallback(
		async (next: string) => {
			const prev = mode;
			try {
				const sid = await ensureSessionId();
				setMode(next);
				const r = await fetch(`/api/sessions/${encodeURIComponent(sid)}/mode`, {
					method: "POST",
					headers: { "Content-Type": "application/json" },
					body: JSON.stringify({ mode: next }),
				});
				if (!r.ok) throw new Error(await r.text());
				const data = await r.json();
				setModes(data.modes ?? []);
				setMode(data.current ?? next);
			} catch (error) {
				setMode(prev);
				toast({
					title: "Could not change mode",
					description: error instanceof Error ? error.message : String(error),
					tone: "error",
				});
			}
		},
		[ensureSessionId, mode, toast],
	);

	const handleCancel = useCallback(
		(clear = false) => {
			if (sessionId) cancel(sessionId, clear);
		},
		[cancel, sessionId],
	);

	const [noticeDismissed, setNoticeDismissed] = useState(false);
	const showNotice = !!capabilities?.notice && !noticeDismissed;
	const handleLeftPanelResize = useCallback(({ inPixels }: PanelSize) => {
		setSidebarCollapsed(inPixels === 0);
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
	const toggleSidebar = useCallback(() => {
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
		(tab: RightTab) => {
			setRequestedRightTab(tab);
			if (rightPanelRef.current?.isCollapsed()) {
				rightPanelRef.current.resize(`${rightPanelWidthRef.current}px`);
			}
		},
		[rightPanelRef],
	);
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
				id: "toggle-sidebar",
				label: "Toggle sidebar",
				icon: <PanelLeftOpen size={12} className="text-fg-dim shrink-0" />,
				run: toggleSidebar,
			},
			{
				id: "toggle-panel",
				label: "Toggle side panel",
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
			actions.push({
				id: "new-terminal",
				label: "New terminal",
				hint: TERMINAL_SHORTCUT,
				icon: <SquareTerminal size={12} className="text-fg-dim shrink-0" />,
				run: () => void createTerminal(),
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
		toggleSidebar,
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

	const canCreateNew = !!(
		sessionId && (sessions[sessionId]?.entries.length ?? 0) > 0
	);
	const leftPanelDocked = !sidebarCollapsed;
	const rightPanelDocked = !rightPanelCollapsed;
	const sidebarContent = (
		<Sidebar
			currentSessionId={sessionId}
			onSessionSelect={(id, disposition) => {
				void handleSessionSelect(id, disposition);
			}}
			onSessionDelete={(id, title) => setSessionDelete({ id, title })}
			runningSessionIds={runningSessionIds}
			switchingAgent={switchingAgent}
			subscribe={subscribe}
		/>
	);
	const workspaceTabs = (
		<div
			className="flex h-10 w-full min-w-0 shrink-0 items-stretch overflow-hidden"
			role="tablist"
			aria-label="Workspace panels"
		>
			<RightTabButton
				active={rightTab === "files"}
				onClick={() => setRequestedRightTab("files")}
			>
				Files
			</RightTabButton>
			{showChanges && (
				<RightTabButton
					active={rightTab === "changes"}
					onClick={() => setRequestedRightTab("changes")}
				>
					Changes
				</RightTabButton>
			)}
			{showProblems && (
				<RightTabButton
					active={rightTab === "problems"}
					onClick={() => setRequestedRightTab("problems")}
				>
					Problems
				</RightTabButton>
			)}
			{showAgents && (
				<RightTabButton
					active={rightTab === "agents"}
					onClick={() => setRequestedRightTab("agents")}
				>
					Agents
				</RightTabButton>
			)}
			<div className="flex-1" />
		</div>
	);
	const inspectorContent = (
		<aside
			className="flex h-full flex-col bg-transparent"
			aria-label="Workspace"
		>
			<div className="min-h-0 flex-1 overflow-hidden pt-0.5" role="tabpanel">
				{rightTab === "agents" && showAgents ? (
					<TasksPanel
						sessionId={sessionId}
						subscribe={subscribe}
						onOpenTask={openTask}
					/>
				) : rightTab === "changes" && showChanges ? (
					<DiffsPanel
						sessionId={sessionId}
						git={capabilities?.git ?? false}
						canInit={capabilities?.git_init ?? false}
						onOpenDiff={openDiff}
						onOpenCompare={openCompare}
						onOpenFile={(path, disposition) =>
							openFile(path, undefined, undefined, undefined, disposition)
						}
						subscribe={subscribe}
					/>
				) : rightTab === "problems" && showProblems ? (
					<ProblemsPanel onOpenFile={openFile} subscribe={subscribe} />
				) : (
					<WorkspaceFilesPanel
						workspaceName={capabilities?.workspace_name ?? "Files"}
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

	return (
		<div
			ref={appRef}
			className="relative flex h-screen flex-col bg-bg text-fg"
			style={
				{
					"--left-panel-width": `${LEFT_PANEL_DEFAULT_SIZE}px`,
					"--right-panel-width": `${rightPanelDefaultWidth}px`,
				} as CSSProperties
			}
		>
			<div
				data-panel-frame="sessions"
				aria-hidden="true"
				className={`pointer-events-none absolute inset-y-0 left-0 z-0 rounded-[10px] bg-bg-surface/40 transition-[transform,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)] ${
					sidebarCollapsed
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
							<AgentPicker
								subscribe={subscribe}
								onSwitchingChange={setSwitchingAgent}
							/>
						</div>
					)}
					{!leftPanelDocked && <div className="min-w-0 flex-1" />}
					{sidebarCollapsed && (
						<button
							type="button"
							className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
							onClick={toggleSidebar}
							title="Show sessions"
							aria-label="Show sessions"
						>
							<PanelLeftOpen size={13} />
						</button>
					)}
				</div>

				<div
					ref={tabListRef}
					className="tab-strip flex min-w-[80px] flex-1 items-stretch overflow-x-auto overscroll-x-contain scrollbar-none"
					role="tablist"
					aria-label="Open tabs"
					onContextMenu={(event) => {
						event.preventDefault();
						const tabElement = (event.target as Element).closest<HTMLElement>(
							"[data-center-tab]",
						);
						const tab = tabs.find(
							(item) => item.id === tabElement?.dataset.centerTab,
						);
						setTabMenu({
							x: event.clientX,
							y: event.clientY,
							tabId: tab && isClosableTab(tab) ? tab.id : undefined,
						});
					}}
				>
					{tabs.map((tab, tabIndex) => {
						const active = tab.id === activeTabId;
						const closable = isClosableTab(tab);
						const isDirty =
							tab.type === "file" && !!tab.path && dirtyPaths.has(tab.path);
						const running =
							tab.type === "chat" && tab.sessionId
								? (sessions[tab.sessionId]?.phase ?? "idle") !== "idle"
								: false;
						const label = tab.type === "chat" ? chatTabLabel(tab) : tab.label;
						return (
							<Tab
								key={tab.id}
								id={tab.id}
								kind={tab.type}
								label={label}
								active={active}
								preview={!!tab.preview}
								closable={closable}
								dirty={isDirty}
								running={running}
								position={tabIndex}
								count={tabs.length}
								onActivate={() => activateTab(tab)}
								onNavigate={(next) => {
									const target = tabs[next];
									activateTab(target);
									requestAnimationFrame(() =>
										tabListRef.current
											?.querySelector<HTMLElement>(
												`[data-center-tab="${CSS.escape(target.id)}"]`,
											)
											?.focus(),
									);
								}}
								onClose={() => void requestCloseTab(tab.id)}
								onKeepOpen={() => keepTab(tab.id)}
							/>
						);
					})}
				</div>
				{tabMenu && (
					<TabContextMenu
						x={tabMenu.x}
						y={tabMenu.y}
						tabId={tabMenu.tabId}
						tabCount={tabs.length}
						preview={!!tabs.find((tab) => tab.id === tabMenu.tabId)?.preview}
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
					{activeTab.type === "file" &&
						activeTab.path &&
						activeDocument &&
						!activeDocument.external &&
						!activeDocument.file?.binary && (
							<button
								type="button"
								className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted disabled:cursor-default disabled:opacity-35 disabled:hover:bg-transparent"
								disabled={
									activeDocument.saving || !dirtyPaths.has(activeTab.path)
								}
								onClick={() => void saveFile(activeTab.path!)}
								title={
									activeDocument.saving
										? "Saving file…"
										: dirtyPaths.has(activeTab.path)
											? "Save file (Ctrl+S)"
											: "No changes to save"
								}
								aria-label="Save file"
							>
								{activeDocument.saving ? (
									<Loader2 size={13} className="animate-spin" />
								) : (
									<Save size={13} />
								)}
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
									[activeTab.id]:
										activeFileView === "preview" ? "code" : "preview",
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
					{showTerminal && (
						<TerminalLauncher
							shells={terminalShells}
							onCreate={(shell) => void createTerminal(shell)}
						/>
					)}
				</div>
				<div
					data-window-interactive
					data-titlebar-right-panel
					className="flex shrink-0 items-center overflow-hidden pr-2 pl-0"
					style={{
						width: rightPanelDocked ? "var(--right-panel-width)" : "40px",
					}}
				>
					{rightPanelCollapsed && (
						<button
							type="button"
							className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
							onClick={toggleRightPanel}
							title="Show workspace panel"
							aria-label="Show workspace panel"
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
				</div>
			</header>
			{showNotice && (
				<div className="flex shrink-0 items-center gap-3 border-b border-warning/30 bg-warning/10 px-4 py-2 text-[12px] text-warning">
					<span className="flex-1">{capabilities?.notice}</span>
					<button
						type="button"
						onClick={() => setNoticeDismissed(true)}
						className="opacity-70 hover:opacity-100 px-1"
						aria-label="Dismiss"
					>
						×
					</button>
				</div>
			)}
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
					inert={sidebarCollapsed}
					className="h-full overflow-hidden"
				>
					<div
						data-panel-content="sessions"
						className={`h-full overflow-hidden transition-[transform,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)] ${
							sidebarCollapsed
								? "pointer-events-none -translate-x-full opacity-0"
								: "translate-x-0 opacity-100"
						}`}
						style={{ width: "var(--left-panel-width)" }}
					>
						{sidebarContent}
					</div>
				</Panel>
				<ResizeHandle label="Resize sessions panel" hidden={sidebarCollapsed} />
				<Panel
					id="center"
					minSize={`${CENTER_PANEL_MIN_SIZE}px`}
					data-layout-panel="center"
					className="flex min-w-0 flex-col overflow-hidden bg-bg"
				>
					<main className="flex flex-1 flex-col overflow-hidden min-h-0 bg-bg">
						<div
							className="flex-1 overflow-hidden"
							onPointerDownCapture={() => {
								if (activeTab.preview) keepTab(activeTab.id);
							}}
							onKeyDownCapture={() => {
								if (activeTab.preview) keepTab(activeTab.id);
							}}
						>
							<ErrorBoundary
								key={activeTab.id}
								fallback={(error, _reset, errorInfo) => (
									<TabCrashed error={error} errorInfo={errorInfo} />
								)}
							>
								{activeTab.type === "chat" ? (
									<ChatPanel
										key={activeTab.id}
										sessionId={activeTab.sessionId ?? ""}
										entries={entries}
										phase={phase}
										modes={modes}
										mode={mode}
										onSelectMode={selectMode}
										onSend={handleSend}
										onCancel={handleCancel}
										pendingInputs={pendingInputs}
										queuePaused={queuePaused}
										canSteer={canSteer}
										onRemoveQueued={(id, state) => {
											if (!sessionId) return;
											if (state === "queued" || state === "sending") {
												removeQueued(sessionId, id);
											} else {
												dismissPending(sessionId, id);
											}
										}}
										onUpdateQueued={(id, text, files, images) =>
											sessionId
												? updateQueued(sessionId, id, text, files, images)
												: false
										}
										onResumeQueue={() => {
											if (sessionId) resumeQueue(sessionId);
										}}
										onClearQueue={() => {
											if (sessionId) clearQueue(sessionId);
										}}
										loading={
											sessionLoad.loading && sessionLoad.id === sessionId
										}
										loadError={
											sessionLoad.id === sessionId ? sessionLoad.error : null
										}
										error={sessionError}
										onDismissError={() => {
											if (sessionId) dismissError(sessionId);
										}}
										subscribe={subscribe}
										prompt={prompt}
										onPromptReply={handlePromptReply}
										seed={composerSeed}
										toolProgress={toolProgress}
									/>
								) : activeTab.type === "terminal" && activeTab.terminalId ? (
									<TerminalView
										key={activeTab.terminalId}
										id={activeTab.terminalId}
										active
										onExit={() => closeTabNow(activeTab.id)}
										onTitle={setTerminalTitle}
									/>
								) : activeTab.type === "task" && activeTab.taskId ? (
									<TaskTab
										key={activeTab.id}
										sessionId={activeTab.sessionId ?? ""}
										taskId={activeTab.taskId}
										subscribe={subscribe}
									/>
								) : activeTab.type === "graph" ? (
									<InsightsTab
										key={activeTab.id}
										onStartAnalysis={(command) =>
											void startInsightsAnalysis(command)
										}
										onOpenFile={(path, line, column) =>
											openFile(path, line, column)
										}
									/>
								) : activeTab.type === "compare" &&
								  activeTab.compareBase &&
								  activeTab.compareHead &&
								  activeTab.compareMode ? (
									<CompareTab
										key={activeTab.id}
										base={activeTab.compareBase}
										head={activeTab.compareHead}
										mode={activeTab.compareMode}
										subscribe={subscribe}
									/>
								) : activeTab.type === "diff" && activeTab.path ? (
									<DiffTab
										path={activeTab.path}
										layer={activeTab.diffLayer}
										sessionId={sessionId}
										subscribe={subscribe}
										onDeleted={() => closeTabNow(activeTab.id)}
									/>
								) : activeTab.path && documents[activeTab.path] ? (
									<FileTab
										key={`${activeTab.id}:${activeTab.path}`}
										document={documents[activeTab.path]}
										tabEnabled={tabEnabled}
										line={activeTab.line}
										column={activeTab.column}
										navigationKey={activeTab.navigationKey}
										subscribe={subscribe}
										onChange={(value) => {
											keepTab(activeTab.id);
											updateDraft(activeTab.path!, value);
										}}
										onSave={async () => {
											return saveFile(activeTab.path!);
										}}
										onReload={() =>
											void reloadDocument(
												activeTab.path!,
												activeTab.external ?? false,
											)
										}
										onOpenFile={openFile}
										onApplyWorkspaceEdit={requestWorkspaceEdit}
										view={
											fileViews[activeTab.id] ?? defaultFileView(activeTab.path)
										}
									/>
								) : null}
							</ErrorBoundary>
						</div>
					</main>
				</Panel>
				<ResizeHandle
					label="Resize workspace panel"
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
						className={`${dialogButtonClass} bg-fg text-bg hover:bg-fg-muted hover:text-bg`}
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
					className={`${dialogButtonClass} bg-fg text-bg hover:bg-fg-muted hover:text-bg`}
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
					className={`${dialogButtonClass} bg-fg text-bg hover:bg-fg-muted hover:text-bg`}
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
					className={`${dialogButtonClass} bg-fg text-bg hover:bg-fg-muted hover:text-bg`}
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
					onClick={() => void deleteSession()}
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

function formatTokens(n: number): string {
	if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
	if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
	return String(n);
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

function RightTabButton({
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

	const label = shells[0]?.name ?? "shell";
	const hasChoices = shells.length > 1;

	return (
		<div
			ref={launcherRef}
			className="relative self-center flex h-8 w-8 shrink-0"
		>
			<button
				type="button"
				className={`flex h-8 items-center justify-center text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted ${
					hasChoices ? "w-5 rounded-l-md" : "w-8 rounded-md"
				}`}
				onClick={() => onCreate()}
				title={`New ${label} terminal`}
			>
				<SquareTerminal size={13} />
			</button>
			{hasChoices && (
				<button
					type="button"
					className="flex h-8 w-3 items-center justify-center rounded-r-md text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted"
					onClick={() => setOpen((value) => !value)}
					title="New terminal with another shell"
					aria-label="Select shell"
					aria-haspopup="menu"
					aria-expanded={open}
				>
					<ChevronDown size={9} />
				</button>
			)}
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
