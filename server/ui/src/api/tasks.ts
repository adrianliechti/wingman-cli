import { isDraft } from "../state/sessionStore.ts";
import { sessionPath } from "../state/workspaceClient.ts";
import type { ScheduleEntry, TaskDetail, TaskEntry } from "../types/protocol";
import { fetchJSON, fetchOK } from "./http.ts";

function sessionAPIBase(sessionId: string): string {
	return sessionPath(sessionId);
}

export function listTasks(
	sessionId: string,
	signal?: AbortSignal,
): Promise<TaskEntry[]> {
	if (isDraft(sessionId)) return Promise.resolve([]);
	return fetchJSON<TaskEntry[]>(`${sessionAPIBase(sessionId)}/tasks`, {
		signal,
	});
}

export function listSchedules(
	sessionId: string,
	signal?: AbortSignal,
): Promise<ScheduleEntry[]> {
	if (isDraft(sessionId)) return Promise.resolve([]);
	return fetchJSON<ScheduleEntry[]>(`${sessionAPIBase(sessionId)}/schedules`, {
		signal,
	});
}

export function getTask(
	sessionId: string,
	taskId: string,
	signal?: AbortSignal,
): Promise<TaskDetail> {
	return fetchJSON<TaskDetail>(
		`${sessionAPIBase(sessionId)}/tasks/${encodeURIComponent(taskId)}`,
		{ signal },
	);
}

export async function deleteSchedule(
	sessionId: string,
	scheduleId: string,
): Promise<void> {
	await fetchOK(
		`${sessionAPIBase(sessionId)}/schedules/${encodeURIComponent(scheduleId)}`,
		{ method: "DELETE" },
	);
}

export async function setSchedulePaused(
	sessionId: string,
	scheduleId: string,
	paused: boolean,
): Promise<void> {
	await fetchOK(
		`${sessionAPIBase(sessionId)}/schedules/${encodeURIComponent(scheduleId)}/${paused ? "pause" : "resume"}`,
		{ method: "POST" },
	);
}

export async function stopTask(
	sessionId: string,
	taskId: string,
): Promise<void> {
	await fetchOK(
		`${sessionAPIBase(sessionId)}/tasks/${encodeURIComponent(taskId)}/stop`,
		{ method: "POST" },
	);
}
