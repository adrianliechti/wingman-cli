import { useQuery } from "@tanstack/react-query";
import { Loader2, MessageSquare, Trash2 } from "lucide-react";
import { useState } from "react";
import {
	useWorkspace,
	backendSettingsQuery,
} from "../state/workspaceContext.ts";
import { splitSessionKey } from "../state/sessionStore.ts";
import { sessionQueries, type SessionInfo } from "../api/sessions";
import type { TabDisposition } from "../types/tabs";
import { FloatingMenu } from "./ui/Floating";

interface Props {
	currentSessionId: string;
	onSessionSelect: (id: string, disposition?: TabDisposition) => void;
	onSessionDelete?: (id: string, title: string) => void;
	runningSessionIds?: Set<string>;
	switchingAgent?: string | null;
}

export function AgentSessions({
	currentSessionId,
	onSessionSelect,
	onSessionDelete,
	runningSessionIds,
	switchingAgent,
}: Props) {
	const { backend } = useWorkspace();
	const sessions = useQuery(sessionQueries.list(backend)).data ?? [];
	const canDelete =
		useQuery(backendSettingsQuery(backend)).data?.canDelete ?? false;

	const [menu, setMenu] = useState<{
		id: string;
		title: string;
		x: number;
		y: number;
	} | null>(null);

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
								const displayTitle =
									s.title || splitSessionKey(s.id).sessionId.substring(0, 8);
								return (
									<div
										key={s.id}
										data-session-id={s.id}
										className={`group relative mx-1.5 flex items-stretch rounded-md text-[12px] transition-colors ${
											active
												? "bg-bg-active text-fg"
												: "text-fg-muted hover:bg-bg-hover hover:text-fg"
										}`}
										onContextMenu={(e) => {
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
											onClick={() => onSessionSelect(s.id, "preview")}
											onDoubleClick={() => onSessionSelect(s.id, "keep")}
											onKeyDown={(event) => {
												if (
													event.key === "Enter" &&
													(event.ctrlKey || event.metaKey)
												) {
													event.preventDefault();
													onSessionSelect(s.id, "keep");
												}
											}}
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
			{menu && (
				<FloatingMenu
					open
					onOpenChange={(open) => !open && setMenu(null)}
					reference={{ x: menu.x, y: menu.y }}
					label={`Actions for ${menu.title}`}
					className="z-[100] min-w-[140px] rounded-md border border-border bg-bg-elevated py-1 shadow-xl"
				>
					<button
						type="button"
						role="menuitem"
						onClick={() => {
							onSessionSelect(menu.id, "keep");
							setMenu(null);
						}}
						className="flex w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-[12px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
					>
						<MessageSquare size={12} className="shrink-0" />
						Open session
					</button>
					{canDelete && <div className="my-1 border-t border-border-subtle" />}
					{canDelete && (
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
					)}
				</FloatingMenu>
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
