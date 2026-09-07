import type { ChatEntry } from "./session.ts";
export type PromptAction = "accept" | "decline" | "cancel";
export type PromptScope = "once" | "session";

export type PromptReply =
	| {
			action: "accept";
			content?: Record<string, unknown>;
			scope?: PromptScope;
	  }
	| { action: Exclude<PromptAction, "accept"> };

export type TurnInputIntent = "follow_up" | "steer";
export type TurnInputState =
	| "sending"
	| "queued"
	| "active"
	| "steered"
	| "completed"
	| "cancelled"
	| "failed";

export type ServerMessage = {
	type:
		| "files_changed"
		| "diffs_changed"
		| "git_index_changed"
		| "sessions_changed"
		| "diagnostics_changed"
		| "capabilities_changed"
		| "model_changed"
		| "tasks_changed"
		| "terminals_changed"
		| "skills_changed";
	session?: string;
	backend?: string;
};

export type Phase = "idle" | "thinking" | "streaming" | "tool_running";

export interface ToolLocation {
	path: string;
	line?: number;
}

export interface FileEntry {
	name: string;
	path: string;
	is_dir: boolean;
	size: number;
}

export interface FileContent {
	path: string;
	content?: string;
	language?: string;
	revision: string;
	binary?: boolean;
	mime?: string;
	size: number;
}

export interface DiffEntry {
	path: string;
	original_path?: string;
	status: "added" | "modified" | "deleted";
	patch: string;
	original?: string;
	modified?: string;
	language?: string;
}

export type DiffLayer = "staged" | "unstaged";
export type CompareMode = "direct" | "merge-base";

export interface GitFileStatus {
	path: string;
	original_path?: string;
	index_status?: string;
	worktree_status?: string;
	staged: boolean;
	changed: boolean;
	conflict?: boolean;
}

export interface GitStatus {
	branch: string;
	upstream?: string;
	ahead: number;
	behind: number;
	has_remote: boolean;
	files: GitFileStatus[];
}

export interface GitBranch {
	name: string;
	remote?: string;
	current?: boolean;
}

export interface GitBranches {
	branches: GitBranch[];
	warning?: string;
}

export interface GitCommit {
	hash: string;
	parents: string[];
	summary: string;
	author: string;
	authored_at: string;
	refs: string[];
}

export interface GitCompare {
	base: string;
	head: string;
	base_hash: string;
	head_hash: string;
	merge_base_hash?: string;
	files: DiffEntry[];
}

export interface TaskEntry {
	id: string;
	description: string;
	agent_type: string;
	status: "running" | "done" | "failed" | "stopped";
	activity?: string;
	elapsed_seconds: number;
	seq: number;
}

export interface ScheduleEntry {
	id: string;
	prompt: string;
	schedule: string;
	status: string;
	script?: boolean;
	next_run?: string;
	next_in?: string;
	last_run?: string;
	failures?: number;
}

export interface TaskDetail extends TaskEntry {
	result?: string;
	transcript: ChatEntry[];
}

export interface TerminalEntry {
	id: string;
	title: string;
	shell: string;
	cols: number;
	rows: number;
	busy: boolean;
}

export interface ShellEntry {
	id: string;
	name: string;
}

export interface DiagnosticEntry {
	path: string;
	line: number;
	column: number;
	end_line?: number;
	end_column?: number;
	severity: "error" | "warning" | "info";
	message: string;
	source?: string;
}

export interface WorkspaceDiagnostics {
	diagnostics: DiagnosticEntry[];
	checked_files: number;
	discovered_files: number;
	discovery_truncated: boolean;
	unknown_files: number;
	unavailable_servers: string[];
	analyzing: boolean;
}

export interface LSPWorkProgress {
	title?: string;
	message?: string;
	percentage?: number;
}

export interface LSPServiceActivity {
	server: string;
	label: string;
	project: string;
	analyzing: boolean;
	operations: LSPWorkProgress[];
}

export interface LSPActivityStatus {
	analyzing: boolean;
	services: LSPServiceActivity[];
}
export type PromptKind = "ask" | "confirm";

export interface PromptField {
	name: string;
	type?: "string" | "number" | "integer" | "boolean";
	title?: string;
	description?: string;
	required?: boolean;
	enum?: string[];
	enum_descriptions?: string[];
	enum_previews?: string[];
	strict?: boolean;
	multiple?: boolean;
	custom_answer_for?: string;
	default?: unknown;
}
