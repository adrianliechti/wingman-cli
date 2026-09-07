import type {
	PromptKind,
	PromptField,
	ToolLocation,
	TurnInputState,
	TurnInputIntent,
} from "./protocol.ts";
export interface PendingPrompt {
	id: string;
	kind: PromptKind;
	message: string;
	fields?: PromptField[];
}

export interface ChatEntry {
	id: string;
	type: "user" | "assistant" | "tool" | "reasoning";
	content: string;
	// Stable source identity; id above is the local React/rendering identity.
	messageId?: string;
	images?: string[];
	files?: string[];
	inputId?: string;
	toolName?: string;
	toolKind?: string;
	toolArgs?: string;
	toolLocations?: ToolLocation[];
	toolHint?: string;
	toolResult?: string;
	toolId?: string;
	toolPartial?: boolean;
	reasoningId?: string;
}

export interface PendingTurnInput {
	id: string;
	state: TurnInputState;
	origin?: string;
	intent: TurnInputIntent;
	position: number;
	text: string;
	files: string[];
	images: string[];
	error?: string;
}
