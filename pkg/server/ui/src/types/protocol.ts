// Client -> Server
export interface SendMessage {
	type: "send";
	text: string;
	files?: string[];
}

export interface CancelMessage {
	type: "cancel";
}

export interface PromptResponseMessage {
	type: "prompt_response";
	approved: boolean;
}

export interface AskResponseMessage {
	type: "ask_response";
	answer: string;
}

export type ClientMessage =
	| SendMessage
	| CancelMessage
	| PromptResponseMessage
	| AskResponseMessage;

// Server -> Client
export interface TextDeltaMessage {
	type: "text_delta";
	text: string;
}

export interface ToolCallMessage {
	type: "tool_call";
	id: string;
	name: string;
	args: string;
	hint: string;
}

export interface ToolResultMessage {
	type: "tool_result";
	id: string;
	name: string;
	content: string;
}

export interface PhaseMessage {
	type: "phase";
	phase: Phase;
	hint?: string;
}

export interface PromptMessage {
	type: "prompt";
	question: string;
}

export interface AskMessage {
	type: "ask";
	question: string;
}

export interface ErrorMessage {
	type: "error";
	message: string;
}

export interface DoneMessage {
	type: "done";
}

export interface UsageMessage {
	type: "usage";
	input_tokens: number;
	output_tokens: number;
}

export interface MessagesMessage {
	type: "messages";
	messages: ConversationMessage[];
}

export type ServerMessage =
	| TextDeltaMessage
	| ToolCallMessage
	| ToolResultMessage
	| PhaseMessage
	| PromptMessage
	| AskMessage
	| ErrorMessage
	| DoneMessage
	| UsageMessage
	| MessagesMessage;

// Shared types
export type Phase = "idle" | "thinking" | "streaming" | "tool_running";

export interface ConversationMessage {
	role: string;
	content: ConversationContent[];
}

export interface ConversationContent {
	text?: string;
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
}

export interface DiagnosticEntry {
	path: string;
	line: number;
	column: number;
	severity: "error" | "warning" | "info";
	message: string;
	source?: string;
}
