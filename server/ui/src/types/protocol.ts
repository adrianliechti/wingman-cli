// Client -> Server
interface SendMessage {
	type: "send";
	text: string;
	files?: string[];
}

interface CancelMessage {
	type: "cancel";
}

interface PromptResponseMessage {
	type: "prompt_response";
	approved: boolean;
}

interface AskResponseMessage {
	type: "ask_response";
	answer: string;
}

export type ClientMessage =
	| SendMessage
	| CancelMessage
	| PromptResponseMessage
	| AskResponseMessage;

// Server -> Client
interface TextDeltaMessage {
	type: "text_delta";
	text: string;
}

interface ReasoningDeltaMessage {
	type: "reasoning_delta";
	id: string;
	text: string;
}

interface ToolCallMessage {
	type: "tool_call";
	id: string;
	name: string;
	args: string;
	hint: string;
}

interface ToolResultMessage {
	type: "tool_result";
	id: string;
	name: string;
	content: string;
}

interface PhaseMessage {
	type: "phase";
	phase: Phase;
	hint?: string;
}

interface PromptMessage {
	type: "prompt";
	question: string;
}

interface AskMessage {
	type: "ask";
	question: string;
}

interface ErrorMessage {
	type: "error";
	message: string;
}

interface DoneMessage {
	type: "done";
}

interface UsageMessage {
	type: "usage";
	input_tokens: number;
	cached_tokens: number;
	output_tokens: number;
}

interface MessagesMessage {
	type: "messages";
	messages: ConversationMessage[];
}

interface SessionMessage {
	type: "session";
	id: string;
}

interface DiffsChangedMessage {
	type: "diffs_changed";
}

interface CheckpointsChangedMessage {
	type: "checkpoints_changed";
}

interface SessionsChangedMessage {
	type: "sessions_changed";
}

interface FilesChangedMessage {
	type: "files_changed";
}

interface DiagnosticsChangedMessage {
	type: "diagnostics_changed";
}

interface CapabilitiesChangedMessage {
	type: "capabilities_changed";
}

export type ServerMessage =
	| TextDeltaMessage
	| ReasoningDeltaMessage
	| ToolCallMessage
	| ToolResultMessage
	| PhaseMessage
	| PromptMessage
	| AskMessage
	| ErrorMessage
	| DoneMessage
	| UsageMessage
	| MessagesMessage
	| SessionMessage
	| DiffsChangedMessage
	| CheckpointsChangedMessage
	| SessionsChangedMessage
	| FilesChangedMessage
	| DiagnosticsChangedMessage
	| CapabilitiesChangedMessage;

// Shared types
export type Phase = "idle" | "thinking" | "streaming" | "tool_running";

export interface ConversationMessage {
	role: string;
	content: ConversationContent[];
}

interface ConversationContent {
	text?: string;
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
	content: string;
	language: string;
}

export interface DiffEntry {
	path: string;
	status: "added" | "modified" | "deleted";
	patch: string;
	original?: string;
	modified?: string;
	language?: string;
}

export interface CheckpointEntry {
	hash: string;
	message: string;
	time: string;
}

export interface DiagnosticEntry {
	path: string;
	line: number;
	column: number;
	severity: "error" | "warning" | "info";
	message: string;
	source?: string;
}
