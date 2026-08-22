import { fetchJSON } from "./http.ts";

export type DebugAction = "run" | "debug";

export interface DebugTarget {
	id: string;
	name: string;
	detail?: string;
	kind: string;
	language: string;
	path: string;
	directory: string;
	line: number;
	column: number;
}

export interface DebugStop {
	reason: string;
	description?: string;
	thread_id?: number;
	all_threads_stopped?: boolean;
	hit_breakpoint_ids?: number[];
}

export interface DebugSession {
	session_id: string;
	adapter: string;
	language: string;
	target?: string;
	mode?: string;
	request: string;
	io: "output" | "terminal";
	terminal_id?: string;
	capabilities: {
		supports_step_back: boolean;
	};
	state_version: number;
	state: "starting" | "configuring" | "running" | "stopped" | "terminated";
	stop?: DebugStop;
	exit_code?: number;
	started_at: string;
	error?: string;
}

export interface DebugPlanBreakpoint {
	file_path: string;
	line: number;
	column?: number;
	condition?: string;
	hit_condition?: string;
	log_message?: string;
}

export interface DebugLaunchPlan {
	action: DebugAction;
	title: string;
	summary: string;
	adapter: string;
	terminal_available: boolean;
	project_dir: string;
	request: "launch" | "attach";
	io: "output" | "terminal";
	configuration: Record<string, unknown>;
	breakpoints: DebugPlanBreakpoint[];
	function_breakpoints: string[];
	prelaunch?: {
		title: string;
		command: string;
		args?: string[];
		ready_url?: string;
	};
}

export interface DebugSourceBreakpoint {
	line: number;
	column?: number;
	condition?: string;
	hit_condition?: string;
	log_message?: string;
}

export interface DebugStackFrame {
	id: number;
	name: string;
	source?: { name?: string; path?: string };
	line: number;
	column: number;
}

export interface DebugState {
	available: boolean;
	session?: DebugSession;
	frame?: DebugStackFrame;
	breakpoints: DebugSourceBreakpoint[];
	frame_error?: string;
}

export interface DebugEvaluation {
	result: string;
	type?: string;
	variables_reference?: number;
	named_variables?: number;
	indexed_variables?: number;
}

export interface DebugBreakpoint {
	id?: number;
	verified: boolean;
	message?: string;
	line?: number;
	column?: number;
}

export interface DebugThread {
	id: number;
	name: string;
}

export interface DebugScope {
	name: string;
	variables_reference: number;
	named_variables?: number;
	indexed_variables?: number;
	expensive?: boolean;
}

export interface DebugVariable {
	name: string;
	value: string;
	type?: string;
	evaluate_name?: string;
	variables_reference?: number;
	named_variables?: number;
	indexed_variables?: number;
}

export interface DebugScopeInspection {
	scope: DebugScope;
	variables: DebugVariable[];
	error?: string;
}

export interface DebugInspection {
	session?: DebugSession;
	output: string;
	threads: DebugThread[];
	frames: DebugStackFrame[];
	error?: string;
}

export async function discoverDebugTargets(
	path: string,
	content: string,
	signal?: AbortSignal,
) {
	return fetchJSON<{ targets: DebugTarget[] }>("/api/debug/targets", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ path, content }),
		signal,
	});
}

export async function generateDebugPlan(
	request: {
		action: DebugAction;
		target_id: string;
		current_path: string;
	},
	signal?: AbortSignal,
) {
	return fetchJSON<DebugLaunchPlan>("/api/debug/plan", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(request),
		signal,
	});
}

export async function startDebugPlan(
	plan: DebugLaunchPlan,
	signal?: AbortSignal,
) {
	return fetchJSON<DebugSession>("/api/debug/start", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(plan),
		signal,
	});
}

export async function getDebugState(path?: string, signal?: AbortSignal) {
	const query = path ? `?path=${encodeURIComponent(path)}` : "";
	return fetchJSON<DebugState>(`/api/debug/state${query}`, { signal });
}

export async function getDebugSession(signal?: AbortSignal) {
	return fetchJSON<{ session?: DebugSession }>("/api/debug/session", {
		signal,
	});
}

export async function getDebugInspection(signal?: AbortSignal) {
	return fetchJSON<DebugInspection>("/api/debug/inspection", { signal });
}

export async function getDebugOutput(signal?: AbortSignal) {
	return fetchJSON<DebugInspection>("/api/debug/inspection?details=false", {
		signal,
	});
}

export async function setDebugBreakpoints(
	path: string,
	breakpoints: DebugSourceBreakpoint[],
	signal?: AbortSignal,
) {
	return fetchJSON<{
		breakpoints: DebugSourceBreakpoint[];
		resolved: DebugBreakpoint[];
	}>("/api/debug/breakpoints", {
		method: "PUT",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ path, breakpoints }),
		signal,
	});
}

export async function controlDebug(
	operation:
		| "continue"
		| "next"
		| "stepIn"
		| "stepOut"
		| "stepBack"
		| "pause"
		| "stop",
	sessionId?: string,
	threadId?: number,
	signal?: AbortSignal,
) {
	return fetchJSON<{ session?: DebugSession }>("/api/debug/control", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({
			operation,
			session_id: sessionId,
			thread_id: threadId,
			wait_timeout_ms: operation === "pause" ? 750 : 150,
		}),
		signal,
	});
}

export async function evaluateDebug(
	expression: string,
	sessionId?: string,
	frameId?: number,
	signal?: AbortSignal,
) {
	return fetchJSON<DebugEvaluation>("/api/debug/evaluate", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({
			expression,
			session_id: sessionId,
			frame_id: frameId,
			context: "hover",
		}),
		signal,
	});
}

export async function getDebugScopes(
	frameId: number,
	sessionId?: string,
	signal?: AbortSignal,
) {
	return fetchJSON<{ scopes: DebugScopeInspection[] }>("/api/debug/scopes", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ frame_id: frameId, session_id: sessionId }),
		signal,
	});
}

export async function getDebugVariables(
	variablesReference: number,
	sessionId?: string,
	signal?: AbortSignal,
) {
	return fetchJSON<{ variables: DebugVariable[] }>("/api/debug/variables", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({
			variables_reference: variablesReference,
			session_id: sessionId,
			count: 200,
		}),
		signal,
	});
}
