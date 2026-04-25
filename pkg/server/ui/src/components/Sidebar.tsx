import { MessageSquare, Plus, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

interface SessionInfo {
	id: string;
	title?: string;
	created_at: string;
	updated_at: string;
}

interface Props {
	currentSessionId: string;
	onSessionSelect: (id: string) => void;
	onNewSession: () => void;
	onSessionDeleted?: (id: string) => void;
}

export function Sidebar({
	currentSessionId,
	onSessionSelect,
	onNewSession,
	onSessionDeleted,
}: Props) {
	const [sessions, setSessions] = useState<SessionInfo[]>([]);

	const loadSessions = useCallback(async () => {
		try {
			const res = await fetch("/api/sessions");
			const data: SessionInfo[] = await res.json();
			setSessions(data);
		} catch {
			setSessions([]);
		}
	}, []);

	useEffect(() => {
		loadSessions();
		const interval = setInterval(loadSessions, 10000);
		return () => clearInterval(interval);
	}, [loadSessions]);

	// biome-ignore lint/correctness/useExhaustiveDependencies: reload when session changes
	useEffect(() => {
		loadSessions();
	}, [currentSessionId, loadSessions]);

	const handleDelete = async (e: React.MouseEvent, id: string) => {
		e.stopPropagation();
		await fetch(`/api/sessions/${id}`, { method: "DELETE" });
		onSessionDeleted?.(id);
		loadSessions();
	};

	const groups = groupSessions(sessions);

	return (
		<div className="w-56 h-full flex flex-col bg-bg shrink-0">
			{/* Header */}
			<div className="h-10 pl-4 pr-1.5 flex items-center justify-between shrink-0">
				<span className="text-[11px] font-medium text-fg-dim uppercase tracking-wider">
					Sessions
				</span>
				<button
					type="button"
					onClick={onNewSession}
					className="w-7 h-7 flex items-center justify-center rounded-md text-fg-dim hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
					title="New session"
				>
					<Plus size={14} />
				</button>
			</div>

			{/* Session List */}
			<div className="flex-1 overflow-y-auto pb-2">
				{groups.length === 0 && (
					<div className="px-3 py-8 text-[11px] text-fg-dim text-center">
						No sessions yet
					</div>
				)}
				{groups.map((group) => (
					<div key={group.label}>
						<div className="px-3 pt-4 pb-1.5">
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
									className={`group relative flex items-center gap-2 px-2.5 py-1.5 mx-1.5 rounded-md cursor-pointer text-[12px] transition-colors ${
										active
											? "bg-bg-active text-fg"
											: "text-fg-muted hover:bg-bg-hover hover:text-fg"
									}`}
									onClick={() => onSessionSelect(s.id)}
									title={s.title || s.id}
								>
									<MessageSquare size={12} className="shrink-0 text-fg-dim" />
									<div className="min-w-0 flex-1">
										<div className="truncate text-[12px] leading-snug">
											{displayTitle}
										</div>
										<div className="text-[10px] text-fg-dim truncate mt-0.5">
											{relativeTime(s.updated_at)}
										</div>
									</div>
									<button
										type="button"
										onClick={(e) => handleDelete(e, s.id)}
										className="w-5 h-5 flex items-center justify-center rounded text-fg-dim hover:text-danger opacity-0 group-hover:opacity-100 shrink-0 transition-all"
										title="Delete session"
									>
										<X size={11} />
									</button>
								</div>
							);
						})}
					</div>
				))}
			</div>
		</div>
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
