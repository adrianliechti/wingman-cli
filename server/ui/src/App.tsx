import {
	Compass,
	FileText,
	GitCompare,
	Loader2,
	MessageSquare,
	PanelLeftClose,
	PanelLeftOpen,
	PanelRightClose,
	PanelRightOpen,
	Plus,
	SquareTerminal,
	Wrench,
	X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	Group,
	Panel,
	type PanelImperativeHandle,
	Separator,
	useDefaultLayout,
	useGroupRef,
	usePanelRef,
} from "react-resizable-panels";
import { ChatPanel } from "./components/ChatPanel";
import {
	CommandPalette,
	type PaletteAction,
	type PaletteSkill,
} from "./components/CommandPalette";
import type { ModeOption } from "./components/ModePicker";
import { DiffsPanel } from "./components/DiffsPanel";
import { DiffTab } from "./components/DiffTab";
import { FileTab } from "./components/FileTab";
import { FileTree } from "./components/FileTree";
import { ProblemsPanel } from "./components/ProblemsPanel";
import { TasksPanel } from "./components/TasksPanel";
import { TerminalPanel } from "./components/TerminalPanel";
import { BUILTIN_AGENT_ID } from "./components/AgentPicker";
import { Sidebar } from "./components/Sidebar";
import { useCapabilities } from "./hooks/useCapabilities";
import {
	type ChatEntry,
	type PromptReply,
	useWebSocket,
} from "./hooks/useWebSocket";
import type { DiffLayer, TurnInputIntent } from "./types/protocol";

interface CenterTab {
	id: string;
	type: "chat" | "file" | "diff";
	label: string;
	path?: string;
	diffLayer?: DiffLayer;
	line?: number;
	sessionId?: string;
}

type RightTab = "changes" | "files" | "agents";

const EMPTY_ENTRIES: never[] = [];
const EMPTY_USAGE = {
	inputTokens: 0,
	cachedTokens: 0,
	outputTokens: 0,
	lastInputTokens: 0,
	contextWindow: 0,
};

const DEFAULT_TERMINAL_PERCENT = 30;

const TERMINAL_SHORTCUT = /Mac|iPhone|iPad/.test(navigator.platform)
	? "⌃⌥T"
	: "Ctrl+Alt+T";

const chatTabId = (sessionId: string) => `chat:${sessionId}`;

function draftChatTab(): CenterTab {
	return {
		id: chatTabId(""),
		type: "chat",
		label: "New Session",
		sessionId: "",
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
	const capabilities = useCapabilities(subscribe);
	const showChanges = capabilities?.diffs ?? false;
	const showProblems = capabilities?.lsp ?? false;
	const showAgents = capabilities?.tasks ?? false;
	const showTerminal = capabilities?.terminal ?? false;
	const [requestedRightTab, setRequestedRightTab] =
		useState<RightTab>("changes");
	const rightTab =
		(requestedRightTab === "changes" && !showChanges) ||
		(requestedRightTab === "agents" && !showAgents)
			? "files"
			: requestedRightTab;
	const [sidebarCollapsed, setSidebarCollapsed] = useState(true);
	const [rightPanelCollapsed, setRightPanelCollapsed] = useState(false);
	const [terminalCollapsed, setTerminalCollapsed] = useState(true);
	const terminalSizeRef = useRef(0);
	const leftPanelRef = usePanelRef();
	const rightPanelRef = usePanelRef();
	const terminalPanelRef = usePanelRef();
	const centerGroupRef = useGroupRef();
	const { defaultLayout, onLayoutChanged } = useDefaultLayout({
		id: "wingman-layout",
	});
	const {
		defaultLayout: centerDefaultLayout,
		onLayoutChanged: onCenterLayoutChanged,
	} = useDefaultLayout({ id: "wingman-center-layout" });

	const [tabs, setTabs] = useState<CenterTab[]>([draftChatTab()]);
	const [activeTabId, setActiveTabId] = useState(chatTabId(""));
	const [currentSessionId, setCurrentSessionId] = useState("");
	const [paletteOpen, setPaletteOpen] = useState(false);
	const [composerSeed, setComposerSeed] = useState<{
		text: string;
		nonce: number;
	} | null>(null);

	const toggleTerminal = useCallback(() => {
		const panel = terminalPanelRef.current;
		if (!panel) return;
		if (!panel.isCollapsed()) {
			panel.collapse();
			return;
		}
		// Expanding a collapsed panel only grows it to its minSize, so set the
		// group layout directly to restore the last height (or the default).
		const size = terminalSizeRef.current || DEFAULT_TERMINAL_PERCENT;
		centerGroupRef.current?.setLayout({
			workspace: 100 - size,
			terminal: size,
		});
	}, [terminalPanelRef, centerGroupRef]);

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
				toggleTerminal();
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [toggleTerminal]);

	const activeTab = tabs.find((t) => t.id === activeTabId) || tabs[0];
	const sessionId =
		activeTab.type === "chat" ? (activeTab.sessionId ?? "") : currentSessionId;

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

	const openChatTab = useCallback(
		(sid: string) => {
			const tab: CenterTab = {
				id: chatTabId(sid),
				type: "chat",
				label: "Session",
				sessionId: sid,
			};
			setTabs((prev) => {
				if (prev.some((t) => t.type === "chat" && t.sessionId === sid)) {
					return prev;
				}
				const draft = prev.findIndex((t) => t.type === "chat" && !t.sessionId);
				if (draft >= 0) {
					const next = [...prev];
					next[draft] = tab;
					return next;
				}
				return [...prev, tab];
			});
			activateTab(tab);
		},
		[activateTab],
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
		(path: string, line?: number) => {
			const existing = tabs.find((t) => t.type === "file" && t.path === path);
			if (existing) {
				if (line) {
					setTabs((prev) =>
						prev.map((t) => (t.id === existing.id ? { ...t, line } : t)),
					);
				}
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
			};
			setTabs((prev) => [...prev, tab]);
			setActiveTabId(tab.id);
		},
		[tabs],
	);

	const openDiff = useCallback(
		(path: string, layer?: DiffLayer) => {
			const existing = tabs.find(
				(t) => t.type === "diff" && t.path === path && t.diffLayer === layer,
			);
			if (existing) {
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
			setTabs((prev) => [...prev, tab]);
			setActiveTabId(tab.id);
		},
		[tabs],
	);

	const closeTab = useCallback(
		(id: string) => {
			const idx = tabs.findIndex((t) => t.id === id);
			if (idx < 0) return;
			setTabs((prev) => {
				let next = prev.filter((t) => t.id !== id);
				if (!next.some((t) => t.type === "chat")) {
					next = [draftChatTab(), ...next];
				}
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
		[tabs, activeTabId, activateTab],
	);

	useEffect(() => {
		if (tabs.some((t) => t.id === activeTabId)) return;
		activateTab(tabs[0] ?? draftChatTab());
	}, [tabs, activeTabId, activateTab]);

	const [dirtyTabs, setDirtyTabs] = useState<Set<string>>(() => new Set());
	const setTabDirty = useCallback((id: string, dirty: boolean) => {
		setDirtyTabs((prev) => {
			const has = prev.has(id);
			if (has === dirty) return prev;
			const next = new Set(prev);
			if (dirty) next.add(id);
			else next.delete(id);
			return next;
		});
	}, []);

	const handleNewSession = useCallback(async () => {
		try {
			const res = await fetch("/api/sessions", { method: "POST" });
			if (!res.ok) return;
			const data = (await res.json()) as { id?: string };
			if (!data.id) return;
			openChatTab(data.id);
		} catch {}
	}, [openChatTab]);

	const handleSessionDeleted = useCallback(
		(id: string) => {
			removeSession(id);
			setCurrentSessionId((prev) => (prev === id ? "" : prev));
			const tab = tabs.find((t) => t.type === "chat" && t.sessionId === id);
			if (tab) closeTab(tab.id);
		},
		[removeSession, tabs, closeTab],
	);

	const [sessionLoad, setSessionLoad] = useState<{
		id: string;
		loading: boolean;
		error: string | null;
	}>({ id: "", loading: false, error: null });
	const loadReqRef = useRef(0);

	const handleSessionSelect = useCallback(
		async (id: string) => {
			openChatTab(id);
			if (hasSession(id)) return;
			const req = ++loadReqRef.current;
			setSessionLoad({ id, loading: true, error: null });
			let error: string | null = null;
			try {
				const res = await fetch(`/api/sessions/${id}/load`, { method: "POST" });
				if (!res.ok) {
					error =
						(await res.text()).trim() ||
						`Failed to load session (${res.status}).`;
				}
			} catch {
				error = "Failed to load session.";
			}
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
		openChatTab(data.id);
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
			} catch {
				setMode(prev);
			}
		},
		[ensureSessionId, mode],
	);

	const handleCancel = useCallback(
		(clear = false) => {
			if (sessionId) cancel(sessionId, clear);
		},
		[cancel, sessionId],
	);

	const [noticeDismissed, setNoticeDismissed] = useState(false);
	const showNotice = !!capabilities?.notice && !noticeDismissed;

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
				run: () => togglePanel(leftPanelRef.current),
			},
			{
				id: "toggle-panel",
				label: "Toggle side panel",
				icon: <PanelRightOpen size={12} className="text-fg-dim shrink-0" />,
				run: () => togglePanel(rightPanelRef.current),
			},
		];
		if (showChanges) {
			actions.push({
				id: "show-changes",
				label: "Show changes",
				icon: <GitCompare size={12} className="text-fg-dim shrink-0" />,
				run: () => {
					setRequestedRightTab("changes");
					expandPanel(rightPanelRef.current);
				},
			});
		}
		if (showTerminal) {
			actions.push({
				id: "toggle-terminal",
				label: "Toggle terminal",
				hint: TERMINAL_SHORTCUT,
				icon: <SquareTerminal size={12} className="text-fg-dim shrink-0" />,
				run: toggleTerminal,
			});
		}
		actions.push({
			id: "show-files",
			label: "Show files",
			icon: <FileText size={12} className="text-fg-dim shrink-0" />,
			run: () => {
				setRequestedRightTab("files");
				expandPanel(rightPanelRef.current);
			},
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
		leftPanelRef,
		rightPanelRef,
		toggleTerminal,
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

	return (
		<div className="relative flex flex-col h-screen bg-bg text-fg">
			{showNotice && (
				<div className="shrink-0 px-4 py-2 text-[12px] flex items-center gap-3 bg-yellow-500/10 border-b border-yellow-500/30 text-yellow-700 dark:text-yellow-300">
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
				orientation="horizontal"
				id="wingman-layout"
				defaultLayout={defaultLayout}
				onLayoutChanged={onLayoutChanged}
				className="flex-1 overflow-hidden"
			>
				<Panel
					panelRef={leftPanelRef}
					id="sidebar"
					defaultSize="0px"
					collapsible
					collapsedSize="0px"
					minSize="224px"
					groupResizeBehavior="preserve-pixel-size"
					onResize={({ inPixels }) => setSidebarCollapsed(inPixels === 0)}
					className="overflow-hidden"
				>
					<Sidebar
						currentSessionId={sessionId}
						onSessionSelect={handleSessionSelect}
						onNewSession={handleNewSession}
						onSessionDeleted={handleSessionDeleted}
						runningSessionIds={runningSessionIds}
						canCreateNew={canCreateNew}
						subscribe={subscribe}
					/>
				</Panel>
				<ResizeHandle />

				<Panel
					id="center"
					minSize="320px"
					className="flex flex-col overflow-hidden min-w-0 bg-bg"
				>
					<Group
						orientation="vertical"
						groupRef={centerGroupRef}
						id="wingman-center-layout"
						defaultLayout={centerDefaultLayout}
						onLayoutChanged={onCenterLayoutChanged}
						className="flex-1 overflow-hidden"
					>
						<Panel
							id="workspace"
							minSize="160px"
							className="flex flex-col overflow-hidden min-h-0 bg-bg"
						>
							<div className="h-10 flex items-stretch bg-bg shrink-0 overflow-x-auto">
								<button
									type="button"
									className="self-center flex items-center justify-center w-8 h-8 ml-1 rounded-md text-fg-dim hover:text-fg-muted hover:bg-bg-hover cursor-pointer transition-colors shrink-0"
									onClick={() => togglePanel(leftPanelRef.current)}
									title={sidebarCollapsed ? "Show sidebar" : "Hide sidebar"}
								>
									{sidebarCollapsed ? (
										<PanelLeftOpen size={13} />
									) : (
										<PanelLeftClose size={13} />
									)}
								</button>
								{sidebarCollapsed && canCreateNew && (
									<button
										type="button"
										className="self-center flex items-center justify-center w-8 h-8 rounded-md text-fg-dim hover:text-fg-muted hover:bg-bg-hover cursor-pointer transition-colors shrink-0"
										onClick={handleNewSession}
										title="New session"
									>
										<Plus size={13} />
									</button>
								)}
								{tabs.map((tab) => {
									const active = tab.id === activeTabId;
									const isDirty = dirtyTabs.has(tab.id);
									const running =
										tab.type === "chat" && tab.sessionId
											? (sessions[tab.sessionId]?.phase ?? "idle") !== "idle"
											: false;
									const Icon =
										tab.type === "chat"
											? MessageSquare
											: tab.type === "diff"
												? GitCompare
												: FileText;
									const label =
										tab.type === "chat" ? chatTabLabel(tab) : tab.label;
									return (
										<div
											key={tab.id}
											className={`group relative flex items-center gap-1.5 px-3 cursor-pointer text-[12px] shrink-0 select-none transition-colors ${
												active ? "text-fg" : "text-fg-dim hover:text-fg-muted"
											}`}
											onClick={() => activateTab(tab)}
										>
											{active && (
												<span className="absolute bottom-0 left-2 right-2 h-[2px] bg-accent rounded-full" />
											)}
											<span className="w-3.5 h-3.5 flex items-center justify-center shrink-0">
												{running ? (
													<Loader2
														size={13}
														className="group-hover:hidden text-accent animate-spin"
													/>
												) : isDirty ? (
													<span
														className={`group-hover:hidden w-2 h-2 rounded-full ${active ? "bg-fg-muted" : "bg-fg-dim"}`}
														aria-label="Unsaved changes"
													/>
												) : (
													<Icon
														size={13}
														className={`group-hover:hidden ${active ? "text-fg-muted" : "text-fg-dim"}`}
													/>
												)}
												<button
													type="button"
													className="hidden group-hover:flex w-3.5 h-3.5 items-center justify-center text-fg-dim hover:text-fg rounded transition-colors"
													onClick={(e) => {
														e.stopPropagation();
														closeTab(tab.id);
													}}
													aria-label="Close tab"
												>
													<X size={11} />
												</button>
											</span>
											<span className="truncate max-w-[200px]">{label}</span>
										</div>
									);
								})}
								<div className="flex-1" />
								{(usage.inputTokens > 0 || outputTokens > 0) && (
									<div className="flex items-center px-3 text-[11px] text-fg-dim tabular-nums whitespace-nowrap">
										{"↑"}
										{formatTokens(usage.inputTokens)}
										{usage.cachedTokens > 0 && (
											<span className="ml-1">
												({formatTokens(usage.cachedTokens)} cached)
											</span>
										)}
										<span className="ml-2">
											{streamEstimate > 0 ? "↓~" : "↓"}
											{formatTokens(outputTokens)}
										</span>
										{usage.contextWindow > 0 && usage.lastInputTokens > 0 && (
											<ContextLeft
												used={usage.lastInputTokens}
												window={usage.contextWindow}
											/>
										)}
									</div>
								)}
								{showTerminal && (
									<button
										type="button"
										className={`self-center flex items-center justify-center w-8 h-8 rounded-md hover:bg-bg-hover cursor-pointer transition-colors shrink-0 ${
											terminalCollapsed
												? "text-fg-dim hover:text-fg-muted"
												: "text-fg-muted"
										}`}
										onClick={toggleTerminal}
										title={`${terminalCollapsed ? "Show" : "Hide"} terminal (${TERMINAL_SHORTCUT})`}
									>
										<SquareTerminal size={13} />
									</button>
								)}
								<button
									type="button"
									className="self-center flex items-center justify-center w-8 h-8 mr-1 rounded-md text-fg-dim hover:text-fg-muted hover:bg-bg-hover cursor-pointer transition-colors shrink-0"
									onClick={() => togglePanel(rightPanelRef.current)}
									title={rightPanelCollapsed ? "Show panel" : "Hide panel"}
								>
									{rightPanelCollapsed ? (
										<PanelRightOpen size={13} />
									) : (
										<PanelRightClose size={13} />
									)}
								</button>
							</div>

							<div className="h-px bg-border-subtle shrink-0" />

							<div className="flex-1 overflow-hidden">
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
								) : activeTab.type === "diff" && activeTab.path ? (
									<DiffTab
										path={activeTab.path}
										layer={activeTab.diffLayer}
										sessionId={sessionId}
										subscribe={subscribe}
										onDeleted={() => closeTab(activeTab.id)}
									/>
								) : activeTab.path ? (
									<FileTab
										key={activeTab.id}
										path={activeTab.path}
										line={activeTab.line}
										subscribe={subscribe}
										onDeleted={() => closeTab(activeTab.id)}
										onDirtyChange={(d) => setTabDirty(activeTab.id, d)}
									/>
								) : null}
							</div>
						</Panel>
						{showTerminal && <VerticalResizeHandle />}
						{showTerminal && (
							<Panel
								panelRef={terminalPanelRef}
								id="terminal"
								defaultSize="0px"
								collapsible
								collapsedSize="0px"
								minSize="120px"
								groupResizeBehavior="preserve-pixel-size"
								onResize={({ inPixels, asPercentage }) => {
									setTerminalCollapsed(inPixels === 0);
									if (inPixels > 0) terminalSizeRef.current = asPercentage;
								}}
								className="overflow-hidden"
							>
								{!terminalCollapsed && (
									<TerminalPanel
										onClose={() => terminalPanelRef.current?.collapse()}
										subscribe={subscribe}
									/>
								)}
							</Panel>
						)}
					</Group>
				</Panel>
				<ResizeHandle />

				<Panel
					panelRef={rightPanelRef}
					id="right"
					defaultSize="288px"
					collapsible
					collapsedSize="0px"
					minSize="288px"
					groupResizeBehavior="preserve-pixel-size"
					onResize={({ inPixels }) => setRightPanelCollapsed(inPixels === 0)}
					className="overflow-hidden"
				>
					<div className="h-full flex flex-col bg-bg">
						<div className="h-10 flex items-stretch shrink-0">
							{showChanges && (
								<RightTabButton
									active={rightTab === "changes"}
									onClick={() => setRequestedRightTab("changes")}
								>
									Changes
								</RightTabButton>
							)}
							<RightTabButton
								active={rightTab === "files"}
								onClick={() => setRequestedRightTab("files")}
							>
								Files
							</RightTabButton>
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
						<div className="h-px bg-border-subtle shrink-0" />
						<div className="flex-1 overflow-hidden">
							{rightTab === "agents" && showAgents ? (
								<TasksPanel sessionId={sessionId} subscribe={subscribe} />
							) : rightTab === "changes" && showChanges ? (
								<DiffsPanel
									sessionId={sessionId}
									git={capabilities?.git ?? false}
									onOpenDiff={openDiff}
									onOpenFile={openFile}
									subscribe={subscribe}
								/>
							) : (
								<div className="flex flex-col h-full">
									<div className="flex-[3] min-h-0 overflow-hidden flex flex-col">
										<FileTree onFileSelect={openFile} subscribe={subscribe} />
									</div>
									{showProblems && (
										<>
											<div className="h-px bg-border-subtle shrink-0" />
											<div className="flex-[1] min-h-0 overflow-hidden">
												<ProblemsPanel
													onOpenFile={openFile}
													subscribe={subscribe}
												/>
											</div>
										</>
									)}
								</div>
							)}
						</div>
					</div>
				</Panel>
			</Group>

			{paletteOpen && (
				<CommandPalette
					sessionId={sessionId}
					onClose={() => setPaletteOpen(false)}
					actions={paletteActions}
					onRunSkill={runSkill}
					onSelectSession={(id) => void handleSessionSelect(id)}
					onOpenFile={openFile}
				/>
			)}

			{!connected && (
				<div className="absolute inset-0 z-50 flex items-center justify-center backdrop-blur-md bg-bg/60">
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

function estimateStreamingTokens(entries: ChatEntry[]): number {
	let chars = 0;
	for (let i = entries.length - 1; i >= 0; i--) {
		const e = entries[i];
		if (e.type !== "reasoning" && e.type !== "assistant") break;
		chars += e.content.length;
	}
	return Math.floor(chars / 4);
}

function togglePanel(panel: PanelImperativeHandle | null) {
	if (!panel) return;
	if (panel.isCollapsed()) panel.expand();
	else panel.collapse();
}

function expandPanel(panel: PanelImperativeHandle | null) {
	panel?.expand();
}

function ResizeHandle() {
	return (
		<Separator className="w-px shrink-0 bg-border-subtle outline-none hover:bg-accent active:bg-accent transition-colors" />
	);
}

function VerticalResizeHandle() {
	return (
		<Separator className="h-px shrink-0 bg-border-subtle outline-none hover:bg-accent active:bg-accent transition-colors" />
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
			className={`relative px-4 text-[11px] font-medium cursor-pointer transition-colors ${
				active ? "text-fg" : "text-fg-dim hover:text-fg-muted"
			}`}
			onClick={onClick}
		>
			{children}
			{active && (
				<span className="absolute left-3 right-3 bottom-0 h-[2px] bg-accent rounded-full" />
			)}
		</button>
	);
}

function ContextLeft({ used, window }: { used: number; window: number }) {
	const left = Math.max(0, Math.round(((window - used) / window) * 100));
	const tone =
		left <= 10 ? "text-danger" : left <= 30 ? "text-warning" : "text-fg-dim";
	return (
		<span
			className={`ml-2 ${tone}`}
			title={`${formatTokens(used)} of ${formatTokens(window)} context used`}
		>
			{left}% left
		</span>
	);
}
