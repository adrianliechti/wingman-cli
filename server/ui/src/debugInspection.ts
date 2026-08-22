import type { DebugInspection } from "./api/debug.ts";

export function debugInspectionPollInterval(
	inspection: DebugInspection | undefined,
	runningInterval: number,
	stoppedInterval: number,
): number | false {
	const state = inspection?.session?.state;
	if (!state || state === "terminated") return false;
	return state === "running" ? runningInterval : stoppedInterval;
}

export function preserveDebugInspection(
	previous: unknown,
	next: DebugInspection,
): DebugInspection {
	const old = previous as DebugInspection | undefined;
	if (!old) return next;
	if (!next.session && old.session?.state === "terminated") return old;
	if (
		!next.session ||
		!old.session ||
		old.session.session_id !== next.session.session_id
	) {
		return next;
	}
	if (next.session.state_version < old.session.state_version) return old;
	if (next.session.state_version > old.session.state_version) return next;
	if (
		(old.frames.length === 0 && next.frames.length > 0) ||
		(old.threads.length === 0 && next.threads.length > 0)
	) {
		return next;
	}

	// Keep the inspector tree stable within one debugger state so expanded
	// variables are not remounted by output-only polling updates.
	if (old.error === next.error && old.session.error === next.session.error) {
		return old;
	}
	return {
		...old,
		error: next.error,
		session: { ...old.session, error: next.session.error },
	};
}

export function preserveDebugOutput(
	previous: unknown,
	next: DebugInspection,
): DebugInspection {
	const old = previous as DebugInspection | undefined;
	if (!old) return next;
	if (!next.session && old.session?.state === "terminated") return old;
	if (
		old.session &&
		next.session &&
		old.session.session_id === next.session.session_id &&
		next.session.state_version < old.session.state_version
	) {
		return old;
	}
	if (!old.output || next.output) return next;
	if (
		next.session?.session_id &&
		next.session.session_id !== old.session?.session_id
	) {
		return next;
	}
	return { ...next, output: old.output };
}
