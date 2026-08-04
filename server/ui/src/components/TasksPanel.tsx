import {
	AlertCircle,
	Bot,
	ChevronDown,
	ChevronRight,
	CircleCheck,
	CircleSlash,
	Loader2,
	Square,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { ServerMessage, TaskDetail, TaskEntry } from "../types/protocol";
import { MarkdownContent } from "./MarkdownContent";

interface Props {
	sessionId: string;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function TasksPanel({ sessionId, subscribe }: Props) {
	const [tasks, setTasks] = useState<TaskEntry[]>([]);
	const [expandedId, setExpandedId] = useState<string | null>(null);
	const [detail, setDetail] = useState<TaskDetail | null>(null);

	const load = useCallback(async () => {
		if (!sessionId) {
			setTasks([]);
			return;
		}
		try {
			const res = await fetch(`/api/sessions/${sessionId}/tasks`);
			if (!res.ok) {
				setTasks([]);
				return;
			}
			setTasks(await res.json());
		} catch {
			setTasks([]);
		}
	}, [sessionId]);

	const loadDetail = useCallback(
		async (taskId: string) => {
			if (!sessionId) return;
			try {
				const res = await fetch(`/api/sessions/${sessionId}/tasks/${taskId}`);
				if (res.ok) setDetail(await res.json());
			} catch {}
		},
		[sessionId],
	);

	useEffect(() => {
		setExpandedId(null);
		setDetail(null);
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "tasks_changed") {
				load();
				if (expandedId) loadDetail(expandedId);
			} else if (
				msg.type === "tool_result" &&
				(msg.name === "agent" ||
					msg.name === "task_send" ||
					msg.name === "task_stop")
			) {
				load();
			}
		});
	}, [subscribe, load, loadDetail, expandedId]);

	const anyRunning = tasks.some((t) => t.status === "running");

	useEffect(() => {
		if (!anyRunning) return;
		const timer = setInterval(() => {
			load();
			if (expandedId) loadDetail(expandedId);
		}, 3000);
		return () => clearInterval(timer);
	}, [anyRunning, load, loadDetail, expandedId]);

	const toggle = (id: string) => {
		if (expandedId === id) {
			setExpandedId(null);
			setDetail(null);
			return;
		}
		setExpandedId(id);
		setDetail(null);
		loadDetail(id);
	};

	const stop = async (id: string) => {
		await fetch(`/api/sessions/${sessionId}/tasks/${id}/stop`, {
			method: "POST",
		});
		load();
		if (expandedId === id) loadDetail(id);
	};

	return (
		<div className="flex flex-col h-full overflow-hidden bg-bg">
			<div className="overflow-y-auto flex-1">
				{tasks.length === 0 && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						No background agents in this session
					</div>
				)}
				{tasks.map((t) => {
					const expanded = t.id === expandedId;
					return (
						<div key={t.id} className="border-b border-border-subtle">
							<div
								className={`flex items-start gap-1.5 px-3 py-2 cursor-pointer text-[11px] transition-colors ${
									expanded
										? "bg-bg-elevated/50 text-fg"
										: "text-fg-muted hover:bg-bg-hover hover:text-fg"
								}`}
								onClick={() => toggle(t.id)}
							>
								{expanded ? (
									<ChevronDown
										size={12}
										className="shrink-0 mt-0.5 text-fg-dim"
									/>
								) : (
									<ChevronRight
										size={12}
										className="shrink-0 mt-0.5 text-fg-dim"
									/>
								)}
								<StatusIcon status={t.status} />
								<div className="min-w-0 flex-1">
									<div className="truncate">{t.description}</div>
									<div className="text-[10px] text-fg-dim font-mono truncate mt-0.5">
										{t.agent_type} · {formatElapsed(t.elapsed_seconds)}
										{t.status !== "running" && ` · ${t.status}`}
										{t.status === "running" && t.activity && ` · ${t.activity}`}
									</div>
								</div>
								{t.status === "running" && (
									<button
										type="button"
										className="shrink-0 w-4 h-4 flex items-center justify-center rounded text-fg-dim hover:text-danger transition-colors"
										title="Stop agent"
										onClick={(e) => {
											e.stopPropagation();
											stop(t.id);
										}}
									>
										<Square size={9} />
									</button>
								)}
							</div>
							{expanded && (
								<div className="px-3 py-2 text-[11px] border-t border-border-subtle">
									{detail?.id === t.id ? (
										<TaskDetailView detail={detail} />
									) : (
										<Loader2 size={12} className="text-fg-dim animate-spin" />
									)}
								</div>
							)}
						</div>
					);
				})}
			</div>
		</div>
	);
}

function TaskDetailView({ detail }: { detail: TaskDetail }) {
	const trail: string[] = [];
	for (const m of detail.transcript) {
		for (const c of m.content) {
			if (c.tool_call) {
				trail.push(
					c.tool_call.hint
						? `${c.tool_call.name} ${c.tool_call.hint}`
						: c.tool_call.name,
				);
			}
		}
	}
	const recentTrail = trail.slice(-15);

	return (
		<div className="flex flex-col gap-1.5">
			{recentTrail.length > 0 && (
				<div className="font-mono text-[10px] text-fg-dim leading-relaxed">
					{trail.length > recentTrail.length && (
						<div>… {trail.length - recentTrail.length} earlier tool calls</div>
					)}
					{recentTrail.map((line, i) => (
						<div key={`${i}-${line}`} className="truncate">
							{line}
						</div>
					))}
				</div>
			)}
			{detail.status === "running" && (
				<div className="flex items-center gap-1.5 text-fg-dim">
					<Loader2 size={11} className="animate-spin shrink-0" />
					<span className="truncate">{detail.activity || "working…"}</span>
				</div>
			)}
			{detail.status !== "running" && detail.result && (
				<div className="text-fg-muted [&_p]:my-1 [&_pre]:my-1 overflow-x-auto">
					<MarkdownContent text={detail.result} />
				</div>
			)}
		</div>
	);
}

function StatusIcon({ status }: { status: TaskEntry["status"] }) {
	switch (status) {
		case "running":
			return (
				<Loader2
					size={12}
					className="shrink-0 mt-0.5 text-accent animate-spin"
				/>
			);
		case "failed":
			return (
				<AlertCircle size={12} className="shrink-0 mt-0.5 text-danger/70" />
			);
		case "stopped":
			return (
				<CircleSlash size={12} className="shrink-0 mt-0.5 text-warning/70" />
			);
		default:
			return (
				<CircleCheck size={12} className="shrink-0 mt-0.5 text-success/70" />
			);
	}
}

export function TasksTabIcon() {
	return <Bot size={12} />;
}

function formatElapsed(seconds: number): string {
	if (seconds < 60) return `${seconds}s`;
	if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
	return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}
