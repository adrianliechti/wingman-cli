import {
	FileText,
	GitCompare,
	Loader2,
	MessageSquare,
	PanelLeftClose,
	PanelLeftOpen,
	PanelRightClose,
	PanelRightOpen,
	X,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { ChatPanel } from "./components/ChatPanel";
import { CheckpointsPanel } from "./components/CheckpointsPanel";
import { DiffsPanel } from "./components/DiffsPanel";
import { DiffTab } from "./components/DiffTab";
import { FileTab } from "./components/FileTab";
import { FileTree } from "./components/FileTree";
import { ProblemsPanel } from "./components/ProblemsPanel";
import { PromptDialog } from "./components/PromptDialog";
import { Sidebar } from "./components/Sidebar";
import { useCapabilities } from "./hooks/useCapabilities";
import { useWebSocket } from "./hooks/useWebSocket";

interface CenterTab {
	id: string;
	type: "chat" | "file" | "diff";
	label: string;
	path?: string;
	line?: number;
}

type RightTab = "changes" | "files";

export default function App() {
	const {
		connected,
		phase,
		entries,
		prompt,
		sendChat,
		cancel,
		respondPrompt,
		respondAsk,
		setEntries,
		subscribe,
	} = useWebSocket();
	const capabilities = useCapabilities(subscribe);
	// `diffs` controls whether the Changes tab is mounted at all (rewind is
	// available everywhere now). `git` controls the *default* tab — in a
	// non-git scratch dir there's nothing useful to show in Changes on first
	// load, so fall through to Files.
	const showChanges = capabilities?.diffs ?? false;
	const inGitRepo = capabilities?.git ?? false;
	const showProblems = capabilities?.lsp ?? false;
	const [sessionId, setSessionId] = useState("");
	const [rightTab, setRightTab] = useState<RightTab>("changes");
	const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
	const [rightPanelCollapsed, setRightPanelCollapsed] = useState(false);

	// The server pushes its current session id on WS connect; mirror it locally
	// so handlers like handleSessionDeleted can recognize the active session
	// even before the user has switched/created one in the UI.
	useEffect(() => {
		return subscribe((msg) => {
			if (msg.type === "session") {
				setSessionId(msg.id);
			}
		});
	}, [subscribe]);

	// Auto-switch the right-panel tab on first load and on git-status flips:
	//   - first load in scratch mode → Files (Changes is empty until edits)
	//   - flip on (agent ran `git init`) → Changes
	//   - flip off (user `rm -rf .git`'d) → Files
	// Only fires on actual flips so manual tab choices persist across reconnects.
	const prevInGit = useRef<boolean | null>(null);
	useEffect(() => {
		if (!capabilities) return;
		const prev = prevInGit.current;
		prevInGit.current = inGitRepo;

		if (prev === null) {
			if (!inGitRepo) setRightTab("files");
			return;
		}
		if (prev !== inGitRepo) {
			setRightTab(inGitRepo ? "changes" : "files");
		}
	}, [capabilities, inGitRepo]);

	// Center tabs: chat is always first, files are added dynamically
	const [tabs, setTabs] = useState<CenterTab[]>([
		{ id: "chat", type: "chat", label: "Session" },
	]);
	const [activeTabId, setActiveTabId] = useState("chat");

	const openFile = useCallback(
		(path: string, line?: number) => {
			const existing = tabs.find((t) => t.type === "file" && t.path === path);
			if (existing) {
				// Update line if provided
				if (line) {
					setTabs((prev) =>
						prev.map((t) => (t.id === existing.id ? { ...t, line } : t)),
					);
				}
				setActiveTabId(existing.id);
				return;
			}
			const label = path.split("/").pop() || path;
			const tab: CenterTab = { id: `file:${path}`, type: "file", label, path, line };
			setTabs((prev) => [...prev, tab]);
			setActiveTabId(tab.id);
		},
		[tabs],
	);

	const openDiff = useCallback(
		(path: string) => {
			const existing = tabs.find((t) => t.type === "diff" && t.path === path);
			if (existing) {
				setActiveTabId(existing.id);
				return;
			}
			const label = path.split("/").pop() || path;
			const tab: CenterTab = { id: `diff:${path}`, type: "diff", label, path };
			setTabs((prev) => [...prev, tab]);
			setActiveTabId(tab.id);
		},
		[tabs],
	);

	const closeTab = useCallback(
		(id: string) => {
			if (id === "chat") return; // can't close chat
			setTabs((prev) => prev.filter((t) => t.id !== id));
			if (activeTabId === id) setActiveTabId("chat");
		},
		[activeTabId],
	);

	const handleNewSession = useCallback(async () => {
		const res = await fetch("/api/sessions/new", { method: "POST" });
		const data = await res.json();
		setSessionId(data.id);
		setEntries([]);
		setActiveTabId("chat");
	}, [setEntries]);

	const handleSessionDeleted = useCallback(
		(id: string) => {
			if (id === sessionId) {
				handleNewSession();
			}
		},
		[sessionId, handleNewSession],
	);

	const handleSessionSelect = useCallback(
		async (id: string) => {
			const res = await fetch(`/api/sessions/${id}/load`, { method: "POST" });
			if (!res.ok) return;
			const messages = await res.json();
			const restored: Array<{
				id: string;
				type: "user" | "assistant" | "tool" | "error";
				content: string;
				toolName?: string;
				toolArgs?: string;
				toolResult?: string;
			}> = [];
			for (const m of messages) {
				for (const c of m.content) {
					if (c.text) {
						restored.push({
							id: crypto.randomUUID(),
							type: m.role === "user" ? "user" : "assistant",
							content: c.text,
						});
					}
					if (c.tool_result) {
						restored.push({
							id: crypto.randomUUID(),
							type: "tool",
							content: "",
							toolName: c.tool_result.name,
							toolArgs: c.tool_result.args,
							toolResult: c.tool_result.content,
						});
					}
				}
			}
			setEntries(restored);
			setSessionId(id);
			setActiveTabId("chat");
		},
		[setEntries],
	);

	const activeTab = tabs.find((t) => t.id === activeTabId) || tabs[0];

	const [noticeDismissed, setNoticeDismissed] = useState(false);
	const showNotice = !!capabilities?.notice && !noticeDismissed;

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
			<div className="flex flex-1 overflow-hidden">
				{/* Left Sidebar */}
				<div
					className="shrink-0 overflow-hidden transition-[width] duration-200 ease-in-out border-r border-border-subtle"
					style={{ width: sidebarCollapsed ? 0 : 224, borderRightWidth: sidebarCollapsed ? 0 : 1 }}
				>
					<div className="w-56 h-full">
						<Sidebar
							currentSessionId={sessionId}
							onSessionSelect={handleSessionSelect}
							onNewSession={handleNewSession}
							onSessionDeleted={handleSessionDeleted}
							subscribe={subscribe}
						/>
					</div>
				</div>

				{/* Center Panel */}
				<div className="flex-1 flex flex-col overflow-hidden min-w-0 bg-bg">
					{/* Tab bar */}
					<div className="h-10 flex items-stretch bg-bg shrink-0 overflow-x-auto">
						<button
							type="button"
							className="flex items-center justify-center w-10 h-10 text-fg-dim hover:text-fg-muted cursor-pointer transition-colors shrink-0"
							onClick={() => setSidebarCollapsed((c) => !c)}
							title={sidebarCollapsed ? "Show sidebar" : "Hide sidebar"}
						>
							{sidebarCollapsed ? (
								<PanelLeftOpen size={15} />
							) : (
								<PanelLeftClose size={15} />
							)}
						</button>
						{tabs.map((tab) => {
							const active = tab.id === activeTabId;
							const Icon =
								tab.type === "chat"
									? MessageSquare
									: tab.type === "diff"
										? GitCompare
										: FileText;
							return (
								<div
									key={tab.id}
									className={`group relative flex items-center gap-1.5 px-3 cursor-pointer text-[12px] shrink-0 select-none transition-colors ${
										active ? "text-fg" : "text-fg-dim hover:text-fg-muted"
									}`}
									onClick={() => setActiveTabId(tab.id)}
								>
									{active && (
										<span className="absolute bottom-0 left-2 right-2 h-[2px] bg-accent rounded-full" />
									)}
									<span className="w-3.5 h-3.5 flex items-center justify-center shrink-0">
										{tab.type === "chat" ? (
											<Icon
												size={13}
												className={active ? "text-fg-muted" : "text-fg-dim"}
											/>
										) : (
											<>
												<Icon
													size={13}
													className={`group-hover:hidden ${active ? "text-fg-muted" : "text-fg-dim"}`}
												/>
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
											</>
										)}
									</span>
									<span className="truncate max-w-[200px]">{tab.label}</span>
								</div>
							);
						})}
						<div className="flex-1" />
						<button
							type="button"
							className="flex items-center justify-center w-10 h-10 text-fg-dim hover:text-fg-muted cursor-pointer transition-colors shrink-0"
							onClick={() => setRightPanelCollapsed((c) => !c)}
							title={rightPanelCollapsed ? "Show panel" : "Hide panel"}
						>
							{rightPanelCollapsed ? (
								<PanelRightOpen size={15} />
							) : (
								<PanelRightClose size={15} />
							)}
						</button>
					</div>

					{/* Divider */}
					<div className="h-px bg-border-subtle shrink-0" />

					{/* Tab content */}
					<div className="flex-1 overflow-hidden">
						{activeTab.type === "chat" ? (
							<ChatPanel
								entries={entries}
								phase={phase}
								onSend={sendChat}
								onCancel={cancel}
							/>
						) : activeTab.type === "diff" && activeTab.path ? (
							<DiffTab
								path={activeTab.path}
								subscribe={subscribe}
								onDeleted={() => closeTab(activeTab.id)}
							/>
						) : activeTab.path ? (
							<FileTab
								path={activeTab.path}
								line={activeTab.line}
								subscribe={subscribe}
								onDeleted={() => closeTab(activeTab.id)}
							/>
						) : null}
					</div>
				</div>

				{/* Right Panel */}
				<div
					className="shrink-0 overflow-hidden transition-[width] duration-200 ease-in-out border-l border-border-subtle"
					style={{ width: rightPanelCollapsed ? 0 : 288, borderLeftWidth: rightPanelCollapsed ? 0 : 1 }}
				>
					<div className="w-72 h-full flex flex-col bg-bg">
						<div className="h-10 flex items-stretch shrink-0">
							{showChanges && (
								<RightTabButton
									active={rightTab === "changes"}
									onClick={() => setRightTab("changes")}
								>
									Changes
								</RightTabButton>
							)}
							<RightTabButton
								active={rightTab === "files"}
								onClick={() => setRightTab("files")}
							>
								Files
							</RightTabButton>
							<div className="flex-1" />
						</div>
						<div className="h-px bg-border-subtle shrink-0" />
						<div className="flex-1 overflow-hidden">
							{rightTab === "changes" && showChanges ? (
								<div className="flex flex-col h-full">
									<div className="flex-[3] min-h-0 overflow-hidden">
										<DiffsPanel
											visible={true}
											onOpenDiff={openDiff}
											subscribe={subscribe}
										/>
									</div>
									<div className="h-px bg-border-subtle shrink-0" />
									<div className="flex-[1] min-h-0 overflow-hidden">
										<CheckpointsPanel visible={true} subscribe={subscribe} />
									</div>
								</div>
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
				</div>
			</div>

			<PromptDialog
				prompt={prompt}
				onPromptResponse={respondPrompt}
				onAskResponse={respondAsk}
			/>

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
