import { Loader2, MessageSquare, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { ServerMessage } from "../types/protocol";

interface SessionInfo {
	id: string;
	title?: string;
	created_at: string;
	updated_at: string;
}

interface Props {
	currentSessionId: string;
	onSessionSelect: (id: string) => void;
	onSessionDelete?: (id: string, title: string) => void;
	runningSessionIds?: Set<string>;
	switchingAgent?: string | null;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function Sidebar({
	currentSessionId,
	onSessionSelect,
	onSessionDelete,
	runningSessionIds,
	switchingAgent,
	subscribe,
}: Props) {
	const [sessions, setSessions] = useState<SessionInfo[]>([]);
	const [canDelete, setCanDelete] = useState(false);

	const loadSessions = useCallback(async () => {
		try {
			const res = await fetch("/api/sessions");
			const data: SessionInfo[] = await res.json();
			setSessions(data);
		} catch {
			setSessions([]);
		}
	}, []);

	const loadAgent = useCallback(() => {
		fetch("/api/agent")
			.then((r) => r.json())
			.then((data) => setCanDelete(data.canDelete ?? false))
			.catch(() => {});
	}, []);

	useEffect(() => {
		loadSessions();
		loadAgent();
	}, [loadSessions, loadAgent]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "sessions_changed") {
				loadSessions();
			} else if (msg.type === "agent_changed") {
				loadAgent();
				loadSessions();
			}
		});
	}, [subscribe, loadSessions, loadAgent]);

	const [menu, setMenu] = useState<{
		id: string;
		title: string;
		x: number;
		y: number;
	} | null>(null);

	useEffect(() => {
		if (!menu) return;
		const close = () => setMenu(null);
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && close();
		window.addEventListener("click", close);
		window.addEventListener("scroll", close, true);
		window.addEventListener("resize", close);
		window.addEventListener("keydown", onKey);
		return () => {
			window.removeEventListener("click", close);
			window.removeEventListener("scroll", close, true);
			window.removeEventListener("resize", close);
			window.removeEventListener("keydown", onKey);
		};
	}, [menu]);

	const groups = groupSessions(sessions);

	return (
		<nav
			className="w-full h-full flex flex-col bg-transparent"
			aria-label="Sessions"
		>
			<div className="flex-1 overflow-y-auto pb-2">
				{switchingAgent && (
					<div className="h-full flex items-center justify-center">
						<Loader2 size={14} className="text-fg-dim animate-spin" />
					</div>
				)}
				{!switchingAgent && groups.length === 0 && (
					<div className="px-3 py-8 text-[11px] text-fg-dim text-center">
						No sessions yet
					</div>
				)}
				{!switchingAgent &&
					groups.map((group, groupIndex) => (
						<div key={group.label}>
							<div
								className={`pr-3 pb-1.5 pl-4 ${groupIndex === 0 ? "pt-2" : "pt-4"}`}
							>
								<span className="text-[10px] font-medium uppercase tracking-wider text-fg-dim">
									{group.label}
								</span>
							</div>
							{group.sessions.map((s) => {
								const active = s.id === currentSessionId;
								const displayTitle = s.title || s.id.substring(0, 8);
								return (
									<div
										key={s.id}
										className={`group relative mx-1.5 flex items-stretch rounded-md text-[12px] transition-colors ${
											active
												? "bg-bg-active text-fg"
												: "text-fg-muted hover:bg-bg-hover hover:text-fg"
										}`}
										onContextMenu={(e) => {
											if (!canDelete) return;
											e.preventDefault();
											setMenu({
												id: s.id,
												title: displayTitle,
												x: e.clientX,
												y: e.clientY,
											});
										}}
									>
										<button
											type="button"
											className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2.5 py-1.5 text-left"
											onClick={() => onSessionSelect(s.id)}
											title={s.title || s.id}
											aria-current={active ? "page" : undefined}
										>
											{runningSessionIds?.has(s.id) ? (
												<Loader2
													size={12}
													className="shrink-0 animate-spin text-accent"
												/>
											) : (
												<MessageSquare
													size={12}
													className="shrink-0 text-fg-dim"
												/>
											)}
											<div className="min-w-0 flex-1">
												<div className="truncate text-[12px] leading-snug">
													{displayTitle}
												</div>
												<div className="mt-0.5 truncate text-[11px] text-fg-dim">
													{relativeTime(s.updated_at)}
												</div>
											</div>
										</button>
									</div>
								);
							})}
						</div>
					))}
			</div>
			{menu &&
				createPortal(
					<div
						role="menu"
						aria-label={`Actions for ${menu.title}`}
						className="fixed z-100 min-w-[140px] rounded-md border border-border bg-bg-elevated py-1 shadow-xl"
						style={{
							top: Math.max(4, Math.min(menu.y, window.innerHeight - 40)),
							left: Math.max(4, Math.min(menu.x, window.innerWidth - 148)),
						}}
						onClick={(e) => e.stopPropagation()}
					>
						<button
							type="button"
							role="menuitem"
							onClick={() => {
								onSessionDelete?.(menu.id, menu.title);
								setMenu(null);
							}}
							className="flex w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-[12px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-danger"
						>
							<Trash2 size={12} className="shrink-0" />
							Delete session
						</button>
					</div>,
					document.body,
				)}
		</nav>
	);
}

interface SessionGroup {
	label: string;
	sessions: SessionInfo[];
}

function groupSessions(sessions: SessionInfo[]): SessionGroup[] {
	if (sessions.length === 0) return [];

	const now = new Date();
	const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
	const yesterday = new Date(today.getTime() - 86400000);
	const weekAgo = new Date(today.getTime() - 7 * 86400000);

	const groups: Record<string, SessionInfo[]> = {};
	const order: string[] = [];

	for (const s of sessions) {
		const d = new Date(s.updated_at);
		let label: string;
		if (Number.isNaN(d.getTime()) || d >= today) {
			label = "Today";
		} else if (d >= yesterday) {
			label = "Yesterday";
		} else if (d >= weekAgo) {
			label = "This Week";
		} else {
			label = "Older";
		}
		if (!groups[label]) {
			groups[label] = [];
			order.push(label);
		}
		groups[label].push(s);
	}

	return order.map((label) => ({ label, sessions: groups[label] }));
}

function relativeTime(value: string): string {
	if (!value) return "";
	const d = new Date(value);
	if (Number.isNaN(d.getTime())) return value;
	const now = new Date();
	const diffMs = now.getTime() - d.getTime();
	const diffMin = Math.floor(diffMs / 60000);

	if (diffMin < 1) return "just now";
	if (diffMin < 60) return `${diffMin}m ago`;
	const diffHrs = Math.floor(diffMin / 60);
	if (diffHrs < 24) return `${diffHrs}h ago`;
	const diffDays = Math.floor(diffHrs / 24);
	if (diffDays < 7) return `${diffDays}d ago`;
	return d.toLocaleDateString([], { month: "short", day: "numeric" });
}
