interface SendMessage {
	type: "send";
	session: string;
	id: string;
	intent?: TurnInputIntent;
	text: string;
	files?: string[];
	images?: string[];
}

interface CancelMessage {
	type: "cancel";
	session: string;
	clear_queue?: boolean;
}

interface QueueRemoveMessage {
	type: "queue_remove";
	session: string;
	id: string;
}

interface QueueUpdateMessage {
	type: "queue_update";
	session: string;
	id: string;
	intent?: TurnInputIntent;
	text: string;
	files?: string[];
	images?: string[];
}

interface QueueSessionMessage {
	type: "queue_resume" | "queue_clear";
	session: string;
}

interface SyncMessage {
	type: "sync";
	sessions: string[];
}

export type PromptAction = "accept" | "decline" | "cancel";

interface PromptResponseMessage {
	type: "prompt_response";
	session: string;
	prompt_id: string;
	text?: string;
	approved?: boolean;
	always?: boolean;
	action?: PromptAction;
	content?: Record<string, unknown>;
}

interface FocusMessage {
	type: "focus";
}

export type ClientMessage =
	| SendMessage
	| CancelMessage
	| QueueRemoveMessage
	| QueueUpdateMessage
	| QueueSessionMessage
	| SyncMessage
	| PromptResponseMessage
	| FocusMessage;

interface SessionMessage {
	session: string;
}

interface SessionStateMessage extends SessionMessage {
	type: "session_state";
	phase: Phase;
	messages?: ConversationMessage[];
	input_tokens?: number;
	cached_tokens?: number;
	output_tokens?: number;
	last_input_tokens?: number;
	context_window?: number;
}

interface TextDeltaMessage extends SessionMessage {
	type: "text_delta";
	id?: string;
	text: string;
}

interface ReasoningDeltaMessage extends SessionMessage {
	type: "reasoning_delta";
	id: string;
	part?: number;
	text: string;
}

interface ToolCallMessage extends SessionMessage {
	type: "tool_call";
	id: string;
	name: string;
	args: string;
	hint: string;
}

interface ToolResultMessage extends SessionMessage {
	type: "tool_result";
	id: string;
	name: string;
	content: string;
}

interface ToolProgressMessage extends SessionMessage {
	type: "tool_progress";
	id: string;
	text?: string;
}

interface StreamResetMessage extends SessionMessage {
	type: "stream_reset";
}

interface StreamCommitMessage extends SessionMessage {
	type: "stream_commit";
}

interface PhaseMessage extends SessionMessage {
	type: "phase";
	phase: Phase;
}

interface UsageMessage extends SessionMessage {
	type: "usage";
	input_tokens: number;
	cached_tokens: number;
	output_tokens: number;
	last_input_tokens?: number;
	context_window?: number;
}

interface ErrorMessage extends SessionMessage {
	type: "error";
	message: string;
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

interface PromptMessage extends SessionMessage {
	type: "prompt";
	prompt_id: string;
	prompt_kind: PromptKind;
	message: string;
	prompt_fields?: PromptField[];
}

interface PromptCancelMessage extends SessionMessage {
	type: "prompt_cancel";
	prompt_id: string;
}

interface SessionsChangedMessage {
	type: "sessions_changed";
}

interface DiffsChangedMessage {
	type: "diffs_changed";
}

interface FilesChangedMessage {
	type: "files_changed";
}

interface DiagnosticsChangedMessage {
	type: "diagnostics_changed";
}

interface TasksChangedMessage {
	type: "tasks_changed";
	session?: string;
}

interface TerminalsChangedMessage {
	type: "terminals_changed";
}

interface CapabilitiesChangedMessage {
	type: "capabilities_changed";
}

interface AgentChangedMessage {
	type: "agent_changed";
}

export type TurnInputIntent = "follow_up" | "steer";
export type TurnInputState =
	| "sending"
	| "queued"
	| "active"
	| "steered"
	| "completed"
	| "cancelled"
	| "failed";

export interface TurnQueueEntry {
	id: string;
	state: TurnInputState;
	intent?: TurnInputIntent;
	position?: number;
	text?: string;
	files?: string[];
	images?: string[];
	image_count?: number;
	error?: string;
}

interface TurnInputMessage extends SessionMessage {
	type: "turn_input";
	id: string;
	state: TurnInputState;
	intent?: TurnInputIntent;
	position?: number;
	text?: string;
	message?: string;
	queue?: TurnQueueEntry[];
}

interface TurnQueueMessage extends SessionMessage {
	type: "turn_queue";
	queue?: TurnQueueEntry[];
	paused?: boolean;
	can_steer?: boolean;
}

interface ModelChangedMessage {
	type: "model_changed";
}

export type ServerMessage =
	| SessionStateMessage
	| TextDeltaMessage
	| ReasoningDeltaMessage
	| ToolCallMessage
	| ToolResultMessage
	| StreamResetMessage
	| StreamCommitMessage
	| ToolProgressMessage
	| PhaseMessage
	| UsageMessage
	| ErrorMessage
	| PromptMessage
	| PromptCancelMessage
	| SessionsChangedMessage
	| DiffsChangedMessage
	| FilesChangedMessage
	| DiagnosticsChangedMessage
	| CapabilitiesChangedMessage
	| AgentChangedMessage
	| ModelChangedMessage
	| TurnInputMessage
	| TurnQueueMessage
	| TasksChangedMessage
	| TerminalsChangedMessage;

export type Phase = "idle" | "thinking" | "streaming" | "tool_running";

export interface ConversationMessage {
	role: string;
	content: ConversationContent[];
}

interface ConversationContent {
	text?: string;
	text_id?: string;
	image?: {
		data: string;
		name?: string;
	};
	reasoning?: {
		id?: string;
		summary?: string;
	};
	tool_call?: {
		id: string;
		name: string;
		args: string;
		hint?: string;
	};
	tool_result?: {
		id?: string;
		name: string;
		args: string;
		content: string;
	};
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
	binary?: boolean;
	mime?: string;
	size: number;
}

export interface DiffEntry {
	path: string;
	status: "added" | "modified" | "deleted";
	patch: string;
	original?: string;
	modified?: string;
	language?: string;
}

export type DiffLayer = "staged" | "unstaged";

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
	transcript: ConversationMessage[];
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
