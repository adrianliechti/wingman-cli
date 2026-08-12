import {
	AlertCircle,
	Bot,
	CircleCheck,
	CircleSlash,
	Clock,
	Loader2,
	PauseCircle,
	Square,
	Trash2,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type {
	ScheduleEntry,
	ServerMessage,
	TaskEntry,
} from "../types/protocol";
import { formatElapsed } from "../utils/tasks";
import { useToast } from "./ui/Feedback";

// Mutating schedule tools whose results should refresh the panel; list_tasks
// is read-only and excluded.
const SCHEDULE_TOOLS = new Set([
	"schedule_task",
	"pause_task",
	"resume_task",
	"remove_task",
]);

interface Props {
	sessionId: string;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
	onOpenTask?: (task: TaskEntry) => void;
}

export function TasksPanel({ sessionId, subscribe, onOpenTask }: Props) {
	const toast = useToast();
	const [tasks, setTasks] = useState<TaskEntry[]>([]);
	const [schedules, setSchedules] = useState<ScheduleEntry[]>([]);
	const [error, setError] = useState<string | null>(null);

	const load = useCallback(async () => {
		if (!sessionId) {
			setTasks([]);
			setSchedules([]);
			setError(null);
			return;
		}
		try {
			const [taskRes, scheduleRes] = await Promise.all([
				fetch(`/api/sessions/${sessionId}/tasks`),
				fetch(`/api/sessions/${sessionId}/schedules`),
			]);
			if (!taskRes.ok) {
				throw new Error(`Could not load agents (${taskRes.status}).`);
			}
			setTasks(await taskRes.json());
			setSchedules(scheduleRes.ok ? await scheduleRes.json() : []);
			setError(null);
		} catch (loadError) {
			setError(
				loadError instanceof Error ? loadError.message : String(loadError),
			);
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
					msg.name === "task_stop" ||
					SCHEDULE_TOOLS.has(msg.name))
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

	// Keep the "in 42m" countdowns honest without polling when nothing is due.
	useEffect(() => {
		if (schedules.length === 0) return;
		const timer = setInterval(load, 30000);
		return () => clearInterval(timer);
	}, [schedules.length, load]);

	const removeSchedule = async (id: string) => {
		try {
			const response = await fetch(
				`/api/sessions/${sessionId}/schedules/${id}`,
				{ method: "DELETE" },
			);
			if (!response.ok) {
				throw new Error(
					(await response.text()).trim() ||
						`Could not remove the scheduled task (${response.status}).`,
				);
			}
			void load();
		} catch (removeError) {
			toast({
				title: "Scheduled task is still there",
				description:
					removeError instanceof Error
						? removeError.message
						: String(removeError),
				tone: "error",
			});
		}
	};

	const stop = async (id: string) => {
		try {
			const response = await fetch(
				`/api/sessions/${sessionId}/tasks/${id}/stop`,
				{ method: "POST" },
			);
			if (!response.ok) {
				throw new Error(
					(await response.text()).trim() ||
						`Could not stop agent (${response.status}).`,
				);
			}
			void load();
		} catch (stopError) {
			toast({
				title: "Agent is still running",
				description:
					stopError instanceof Error ? stopError.message : String(stopError),
				tone: "error",
			});
		}
	};

	return (
		<div className="flex h-full flex-col overflow-hidden bg-transparent">
			<div className="overflow-y-auto flex-1">
				{error && (
					<div className="mx-2 mt-2 rounded bg-danger/5 px-2 py-1.5 text-[10px] text-danger/80">
						{error}
					</div>
				)}
				{tasks.length === 0 && schedules.length === 0 && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						No background agents or scheduled tasks in this session
					</div>
				)}
				{schedules.length > 0 && (
					<div className="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wide text-fg-dim">
						Scheduled
					</div>
				)}
				{schedules.map((s) => (
					<div
						key={s.id}
						className="group relative flex items-stretch border-b border-border-subtle text-[11px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
					>
						<div
							className="flex min-w-0 flex-1 items-start gap-2 px-3 py-2 pr-9 text-left"
							title={s.prompt}
						>
							{s.status === "paused" ? (
								<PauseCircle
									size={12}
									className="mt-0.5 shrink-0 text-fg-dim"
								/>
							) : (
								<Clock size={12} className="mt-0.5 shrink-0 text-accent" />
							)}
							<div className="min-w-0 flex-1">
								<div className="truncate">{s.prompt}</div>
								<div className="mt-0.5 truncate font-mono text-[11px] text-fg-dim">
									{s.schedule}
									{s.status === "paused"
										? " · paused"
										: s.next_in && ` · ${s.next_in}`}
									{s.script && " · pre-check"}
									{s.failures ? ` · failing x${s.failures}` : ""}
								</div>
							</div>
						</div>
						<button
							type="button"
							className="absolute right-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded text-fg-dim transition-colors hover:bg-bg-active hover:text-danger"
							title="Remove scheduled task"
							onClick={() => void removeSchedule(s.id)}
						>
							<Trash2 size={11} />
						</button>
					</div>
				))}
				{schedules.length > 0 && tasks.length > 0 && (
					<div className="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wide text-fg-dim">
						Agents
					</div>
				)}
				{tasks.map((t) => (
					<div
						key={t.id}
						className="group relative flex items-stretch border-b border-border-subtle text-[11px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
					>
						<button
							type="button"
							className="flex min-w-0 flex-1 items-start gap-2 px-3 py-2 pr-9 text-left"
							onClick={() => onOpenTask?.(t)}
							title={t.description}
						>
							<TaskStatusIcon status={t.status} className="mt-0.5" />
							<div className="min-w-0 flex-1">
								<div className="truncate">{t.description}</div>
								<div className="mt-0.5 truncate font-mono text-[11px] text-fg-dim">
									{t.agent_type} · {formatElapsed(t.elapsed_seconds)}
									{t.status !== "running" && ` · ${t.status}`}
									{t.status === "running" && t.activity && ` · ${t.activity}`}
								</div>
							</div>
						</button>
						{t.status === "running" && (
							<button
								type="button"
								className="absolute right-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded text-fg-dim transition-colors hover:bg-bg-active hover:text-danger"
								title="Stop agent"
								onClick={() => void stop(t.id)}
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
