import {
	AlertCircle,
	Bot,
	CircleCheck,
	CircleSlash,
	Loader2,
	Square,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { ServerMessage, TaskEntry } from "../types/protocol";
import { formatElapsed } from "../utils/tasks";

interface Props {
	sessionId: string;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
	onOpenTask?: (task: TaskEntry) => void;
}

export function TasksPanel({ sessionId, subscribe, onOpenTask }: Props) {
	const [tasks, setTasks] = useState<TaskEntry[]>([]);

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

	useEffect(() => {
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "tasks_changed") {
				load();
			} else if (
				msg.type === "tool_result" &&
				(msg.name === "agent" ||
					msg.name === "task_send" ||
					msg.name === "task_stop")
			) {
				load();
			}
		});
	}, [subscribe, load]);

	const anyRunning = tasks.some((t) => t.status === "running");

	useEffect(() => {
		if (!anyRunning) return;
		const timer = setInterval(load, 3000);
		return () => clearInterval(timer);
	}, [anyRunning, load]);

	const stop = async (id: string) => {
		await fetch(`/api/sessions/${sessionId}/tasks/${id}/stop`, {
			method: "POST",
		});
		load();
	};

	return (
		<div className="flex flex-col h-full overflow-hidden bg-bg">
			<div className="overflow-y-auto flex-1">
				{tasks.length === 0 && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						No background agents in this session
					</div>
				)}
				{tasks.map((t) => (
					<div
						key={t.id}
						className="flex items-start gap-2 px-3 py-2 cursor-pointer text-[11px] text-fg-muted hover:bg-bg-hover hover:text-fg border-b border-border-subtle transition-colors"
						onClick={() => onOpenTask?.(t)}
						title={t.description}
					>
						<TaskStatusIcon status={t.status} className="mt-0.5" />
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
				))}
			</div>
		</div>
	);
}

function TaskStatusIcon({
	status,
	size = 12,
	className = "",
}: {
	status: TaskEntry["status"];
	size?: number;
	className?: string;
}) {
	switch (status) {
		case "running":
			return (
				<Loader2
					size={size}
					className={`shrink-0 text-accent animate-spin ${className}`}
				/>
			);
		case "failed":
			return (
				<AlertCircle
					size={size}
					className={`shrink-0 text-danger/70 ${className}`}
				/>
			);
		case "stopped":
			return (
				<CircleSlash
					size={size}
					className={`shrink-0 text-warning/70 ${className}`}
				/>
			);
		default:
			return (
				<CircleCheck
					size={size}
					className={`shrink-0 text-success/70 ${className}`}
				/>
			);
	}
}

export function TasksTabIcon() {
	return <Bot size={12} />;
}
