export type DebugAction = "run" | "debug";

export interface DebugAdapter {
	name: string;
	language: string;
	projects: string[];
	integrated_terminal: boolean;
}

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
}

export interface DebugSession {
	session_id: string;
	adapter: string;
	language: string;
	target?: string;
	mode?: string;
	request: string;
	console: "internalConsole" | "integratedTerminal";
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

export interface DebugDiscovery {
	adapters: DebugAdapter[];
	targets: DebugTarget[];
	session?: DebugSession;
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
	project_dir: string;
	request: "launch" | "attach";
	console: "internalConsole" | "integratedTerminal";
	configuration: Record<string, unknown>;
	breakpoints: DebugPlanBreakpoint[];
	function_breakpoints: string[];
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
	scopes: DebugScopeInspection[];
	error?: string;
}

export async function discoverDebug(path?: string, signal?: AbortSignal) {
	const query = path ? `?path=${encodeURIComponent(path)}` : "";
	return requestJSON<DebugDiscovery>(`/api/debug/discovery${query}`, {
		signal,
	});
}

export async function discoverDebugTargets(
	path: string,
	content: string,
	signal?: AbortSignal,
) {
	return requestJSON<{ targets: DebugTarget[] }>("/api/debug/targets", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ path, content }),
		signal,
	});
}

export async function generateDebugPlan(
	request: {
		action: DebugAction;
		adapter?: string;
		target_id?: string;
		current_path?: string;
	},
	signal?: AbortSignal,
) {
	return requestJSON<DebugLaunchPlan>("/api/debug/plan", {
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
	return requestJSON<DebugSession>("/api/debug/start", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(plan),
		signal,
	});
}

export async function getDebugState(path?: string, signal?: AbortSignal) {
	const query = path ? `?path=${encodeURIComponent(path)}` : "";
	return requestJSON<DebugState>(`/api/debug/state${query}`, { signal });
}

export async function getDebugInspection(signal?: AbortSignal) {
	return requestJSON<DebugInspection>("/api/debug/inspection", { signal });
}

export async function setDebugBreakpoints(
	path: string,
	breakpoints: DebugSourceBreakpoint[],
	signal?: AbortSignal,
) {
	return requestJSON<{ breakpoints: DebugSourceBreakpoint[] }>(
		"/api/debug/breakpoints",
		{
			method: "PUT",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ path, breakpoints }),
			signal,
		},
	);
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
	return requestJSON<{ session?: DebugSession }>("/api/debug/control", {
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
	return requestJSON<DebugEvaluation>("/api/debug/evaluate", {
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
	return requestJSON<{ scopes: DebugScopeInspection[] }>("/api/debug/scopes", {
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
	return requestJSON<{ variables: DebugVariable[] }>("/api/debug/variables", {
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

async function requestJSON<T>(input: RequestInfo | URL, init?: RequestInit) {
	const response = await fetch(input, init);
	if (!response.ok) {
		const message = (await response.text()).trim();
		throw new Error(message || `Request failed (${response.status})`);
	}
	return (await response.json()) as T;
}
