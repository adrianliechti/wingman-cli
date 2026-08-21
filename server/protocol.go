package server

import "github.com/adrianliechti/wingman-agent/pkg/agent/tool"

const (
	MsgSend           = "send"
	MsgCancel         = "cancel"
	MsgQueueRemove    = "queue_remove"
	MsgQueueUpdate    = "queue_update"
	MsgQueueResume    = "queue_resume"
	MsgQueueClear     = "queue_clear"
	MsgSync           = "sync"
	MsgPromptResponse = "prompt_response"
	MsgFocus          = "focus"
)

type ClientMessage struct {
	Type       string   `json:"type"`
	SessionID  string   `json:"session,omitempty"`
	Text       string   `json:"text,omitempty"`
	Files      []string `json:"files,omitempty"`
	Images     []string `json:"images,omitempty"`
	ID         string   `json:"id,omitempty"`
	Intent     string   `json:"intent,omitempty"`
	ClearQueue bool     `json:"clear_queue,omitempty"`
	Sessions   []string `json:"sessions,omitempty"`

	PromptID string         `json:"prompt_id,omitempty"`
	Approved bool           `json:"approved,omitempty"`
	Always   bool           `json:"always,omitempty"`
	Action   string         `json:"action,omitempty"`
	Content  map[string]any `json:"content,omitempty"`
}

const (
	EvtSessionState        = "session_state"
	EvtTextDelta           = "text_delta"
	EvtReasoningDelta      = "reasoning_delta"
	EvtToolCall            = "tool_call"
	EvtToolResult          = "tool_result"
	EvtToolProgress        = "tool_progress"
	EvtStreamReset         = "stream_reset"
	EvtStreamCommit        = "stream_commit"
	EvtPhase               = "phase"
	EvtUsage               = "usage"
	EvtError               = "error"
	EvtPrompt              = "prompt"
	EvtPromptCancel        = "prompt_cancel"
	EvtFilesChanged        = "files_changed"
	EvtDiffsChanged        = "diffs_changed"
	EvtSessionsChanged     = "sessions_changed"
	EvtDiagnosticsChanged  = "diagnostics_changed"
	EvtCapabilitiesChanged = "capabilities_changed"
	EvtAgentChanged        = "agent_changed"
	EvtModelChanged        = "model_changed"
	EvtTurnInput           = "turn_input"
	EvtTurnQueue           = "turn_queue"
	EvtTasksChanged        = "tasks_changed"
	EvtTerminalsChanged    = "terminals_changed"
	EvtBrowserChanged      = "browser_changed"
)

const (
	PromptKindAsk     = "ask"
	PromptKindConfirm = "confirm"
)

type Frame struct {
	Type    string `json:"type"`
	Session string `json:"session,omitempty"`

	Text     string           `json:"text,omitempty"`
	ID       string           `json:"id,omitempty"`
	Name     string           `json:"name,omitempty"`
	Args     string           `json:"args,omitempty"`
	Hint     string           `json:"hint,omitempty"`
	Content  string           `json:"content,omitempty"`
	Phase    string           `json:"phase,omitempty"`
	Message  string           `json:"message,omitempty"`
	State    string           `json:"state,omitempty"`
	Intent   string           `json:"intent,omitempty"`
	Part     int              `json:"part,omitempty"`
	Position int              `json:"position,omitempty"`
	Paused   bool             `json:"paused,omitempty"`
	CanSteer bool             `json:"can_steer,omitempty"`
	Partial  bool             `json:"partial,omitempty"`
	Queue    []TurnQueueEntry `json:"queue,omitempty"`

	PromptID     string             `json:"prompt_id,omitempty"`
	PromptKind   string             `json:"prompt_kind,omitempty"`
	PromptFields []tool.ElicitField `json:"prompt_fields,omitempty"`

	InputTokens  int64 `json:"input_tokens,omitempty"`
	CachedTokens int64 `json:"cached_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`

	LastInputTokens int64 `json:"last_input_tokens,omitempty"`
	ContextWindow   int64 `json:"context_window,omitempty"`

	Messages []ConversationMessage `json:"messages,omitempty"`
}

type TurnQueueEntry struct {
	ID         string   `json:"id"`
	State      string   `json:"state"`
	Intent     string   `json:"intent"`
	Position   int      `json:"position,omitempty"`
	Text       string   `json:"text,omitempty"`
	Files      []string `json:"files,omitempty"`
	Images     []string `json:"images,omitempty"`
	ImageCount int      `json:"image_count,omitempty"`
}

type ConversationMessage struct {
	Role    string                `json:"role"`
	Content []ConversationContent `json:"content"`
}

type ConversationContent struct {
	Text       string                 `json:"text,omitempty"`
	TextID     string                 `json:"text_id,omitempty"`
	Image      *ConversationImage     `json:"image,omitempty"`
	Reasoning  *ConversationReasoning `json:"reasoning,omitempty"`
	ToolCall   *ConversationTool      `json:"tool_call,omitempty"`
	ToolResult *ConversationResult    `json:"tool_result,omitempty"`
}

type ConversationImage struct {
	Data string `json:"data"`
	Name string `json:"name,omitempty"`
}

type ConversationReasoning struct {
	ID      string `json:"id,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type ConversationTool struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
	Hint string `json:"hint,omitempty"`
}

type ConversationResult struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Args    string `json:"args,omitempty"`
	Content string `json:"content"`
}

type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

type FileContent struct {
	Path     string `json:"path"`
	Content  string `json:"content,omitempty"`
	Language string `json:"language,omitempty"`
	Revision string `json:"revision"`

	Binary bool   `json:"binary,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Size   int64  `json:"size"`
}

type DiffEntry struct {
	Path         string `json:"path"`
	OriginalPath string `json:"original_path,omitempty"`
	Status       string `json:"status"`
	Patch        string `json:"patch"`
	Original     string `json:"original,omitempty"`
	Modified     string `json:"modified,omitempty"`
	Language     string `json:"language,omitempty"`
}

type SessionEntry struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
