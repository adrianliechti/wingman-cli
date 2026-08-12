import {
	AlertCircle,
	Bot,
	CircleCheck,
	CircleSlash,
	Clock,
	Eye,
	Loader2,
	Pause,
	PauseCircle,
	Play,
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
import { Dialog, dialogButtonClass, useToast } from "./ui/Feedback";
import { FloatingMenu } from "./ui/Floating";

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

type MenuState =
	| { x: number; y: number; kind: "schedule"; schedule: ScheduleEntry }
	| { x: number; y: number; kind: "task"; task: TaskEntry };

export function TasksPanel({ sessionId, subscribe, onOpenTask }: Props) {
	const toast = useToast();
	const [tasks, setTasks] = useState<TaskEntry[]>([]);
	const [schedules, setSchedules] = useState<ScheduleEntry[]>([]);
	const [error, setError] = useState<string | null>(null);
	const [menu, setMenu] = useState<MenuState | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<ScheduleEntry | null>(null);

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

	const setScheduleStatus = async (id: string, action: "pause" | "resume") => {
		try {
			const response = await fetch(
				`/api/sessions/${sessionId}/schedules/${id}/${action}`,
				{ method: "POST" },
			);
			if (!response.ok) {
				throw new Error(
					(await response.text()).trim() ||
						`Could not ${action} the scheduled task (${response.status}).`,
				);
			}
			void load();
		} catch (statusError) {
			toast({
				title: `Scheduled task was not ${action}d`,
				description:
					statusError instanceof Error
						? statusError.message
						: String(statusError),
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
		<div className="relative flex h-full flex-col overflow-hidden bg-transparent">
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
						className="group flex items-stretch border-b border-border-subtle text-[11px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
						onContextMenu={(event) => {
							event.preventDefault();
							setMenu({
								x: event.clientX,
								y: event.clientY,
								kind: "schedule",
								schedule: s,
							});
						}}
					>
						<button
							type="button"
							className="group/action ml-2 flex w-5 shrink-0 items-center justify-center text-fg-dim transition-colors hover:text-fg focus-visible:text-fg"
							title={
								s.status === "paused"
									? "Resume scheduled task"
									: "Pause scheduled task"
							}
							onClick={() =>
								void setScheduleStatus(
									s.id,
									s.status === "paused" ? "resume" : "pause",
								)
							}
						>
							<span className="group-hover:hidden group-focus/action:hidden">
								{s.status === "paused" ? (
									<PauseCircle size={12} />
								) : (
									<Clock size={12} className="text-accent" />
								)}
							</span>
							<span className="hidden items-center justify-center group-hover:flex group-focus/action:flex">
								{s.status === "paused" ? (
									<Play size={11} />
								) : (
									<Pause size={11} />
								)}
							</span>
						</button>
						<div
							className="min-w-0 flex-1 py-2 pl-1 pr-3 text-left"
							title={s.prompt}
						>
							<div className="min-w-0">
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
						className="group flex items-stretch border-b border-border-subtle text-[11px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
						onContextMenu={(event) => {
							event.preventDefault();
							setMenu({
								x: event.clientX,
								y: event.clientY,
								kind: "task",
								task: t,
							});
						}}
					>
						{t.status === "running" ? (
							<button
								type="button"
								className="group/action ml-2 flex w-5 shrink-0 items-center justify-center text-fg-dim transition-colors hover:text-danger focus-visible:text-danger"
								title="Stop agent"
								onClick={() => void stop(t.id)}
							>
								<span className="group-hover:hidden group-focus/action:hidden">
									<TaskStatusIcon status={t.status} />
								</span>
								<span className="hidden items-center justify-center group-hover:flex group-focus/action:flex">
									<Square size={9} />
								</span>
							</button>
						) : (
							<span className="ml-2 flex w-5 shrink-0 items-center justify-center">
								<TaskStatusIcon status={t.status} />
							</span>
						)}
						<button
							type="button"
							className="min-w-0 flex-1 py-2 pl-1 pr-3 text-left"
							onClick={() => onOpenTask?.(t)}
							title={t.description}
						>
							<div className="min-w-0">
								<div className="truncate">{t.description}</div>
								<div className="mt-0.5 truncate font-mono text-[11px] text-fg-dim">
									{t.agent_type} · {formatElapsed(t.elapsed_seconds)}
									{t.status !== "running" && ` · ${t.status}`}
									{t.status === "running" && t.activity && ` · ${t.activity}`}
								</div>
							</div>
						</button>
					</div>
				))}
			</div>
			{menu && (
				<AgentContextMenu
					menu={menu}
					onClose={() => setMenu(null)}
					onOpenTask={onOpenTask}
					onRemoveSchedule={(schedule) => setDeleteTarget(schedule)}
					onSetScheduleStatus={(id, action) =>
						void setScheduleStatus(id, action)
					}
					onStopTask={(id) => void stop(id)}
				/>
			)}
			<Dialog
				open={deleteTarget !== null}
				title="Delete scheduled task?"
				description={
					deleteTarget
						? `“${deleteTarget.prompt}” will no longer run.`
						: undefined
				}
				onClose={() => setDeleteTarget(null)}
			>
				<button
					type="button"
					className={dialogButtonClass}
					onClick={() => setDeleteTarget(null)}
				>
					Cancel
				</button>
				<button
					type="button"
					className={`${dialogButtonClass} border-danger/40 text-danger hover:bg-danger/10`}
					onClick={() => {
						const target = deleteTarget;
						setDeleteTarget(null);
						if (target) void removeSchedule(target.id);
					}}
				>
					Delete
				</button>
			</Dialog>
		</div>
	);
}

function AgentContextMenu({
	menu,
	onClose,
	onOpenTask,
	onRemoveSchedule,
	onSetScheduleStatus,
	onStopTask,
}: {
	menu: MenuState;
	onClose: () => void;
	onOpenTask?: (task: TaskEntry) => void;
	onRemoveSchedule: (schedule: ScheduleEntry) => void;
	onSetScheduleStatus: (id: string, action: "pause" | "resume") => void;
	onStopTask: (id: string) => void;
}) {
	return (
		<FloatingMenu
			open
			onOpenChange={(open) => !open && onClose()}
			reference={{ x: menu.x, y: menu.y }}
			label="Agent actions"
			className="z-[100] min-w-[160px] rounded-md border border-border-subtle bg-bg-elevated py-1 text-[12px] shadow-2xl"
		>
			{menu.kind === "task" ? (
				<>
					<MenuItem
						icon={<Eye size={12} />}
						label="Open"
						onClick={() => {
							onClose();
							onOpenTask?.(menu.task);
						}}
					/>
					{menu.task.status === "running" && (
						<>
							<div
								role="separator"
								className="my-1 border-t border-border-subtle"
							/>
							<MenuItem
								icon={<Square size={9} />}
								label="Stop"
								danger
								onClick={() => {
									onClose();
									onStopTask(menu.task.id);
								}}
							/>
						</>
					)}
				</>
			) : (
				<>
					{menu.schedule.status === "paused" ? (
						<MenuItem
							icon={<Play size={12} />}
							label="Resume"
							onClick={() => {
								onClose();
								onSetScheduleStatus(menu.schedule.id, "resume");
							}}
						/>
					) : (
						<MenuItem
							icon={<Pause size={12} />}
							label="Pause"
							onClick={() => {
								onClose();
								onSetScheduleStatus(menu.schedule.id, "pause");
							}}
						/>
					)}
					<div
						role="separator"
						className="my-1 border-t border-border-subtle"
					/>
					<MenuItem
						icon={<Trash2 size={12} />}
						label="Delete"
						danger
						onClick={() => {
							onClose();
							onRemoveSchedule(menu.schedule);
						}}
					/>
				</>
			)}
		</FloatingMenu>
	);
}

function MenuItem({
	icon,
	label,
	onClick,
	danger = false,
}: {
	icon: React.ReactNode;
	label: string;
	onClick: () => void;
	danger?: boolean;
}) {
	return (
		<button
			type="button"
			role="menuitem"
			className={`flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors hover:bg-bg-hover ${danger ? "text-danger" : "text-fg-muted hover:text-fg"}`}
			onClick={onClick}
		>
			<span className="flex w-3.5 shrink-0 items-center justify-center">
				{icon}
			</span>
			<span>{label}</span>
		</button>
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
