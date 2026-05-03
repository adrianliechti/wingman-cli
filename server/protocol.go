package server

// Client -> Server message types
const (
	MsgSend           = "send"
	MsgCancel         = "cancel"
	MsgPromptResponse = "prompt_response"
	MsgAskResponse    = "ask_response"
)

// ClientMessage is the envelope for all client-to-server WebSocket messages.
// Inbound messages share one struct because the type isn't known until the
// frame is unmarshaled.
type ClientMessage struct {
	Type     string   `json:"type"`
	Text     string   `json:"text,omitempty"`
	Files    []string `json:"files,omitempty"`
	Approved bool     `json:"approved,omitempty"`
	Answer   string   `json:"answer,omitempty"`
}

// ServerEvent is implemented by every outbound WebSocket event. sendMessage
// emits the wire payload as {"type": <serverEventType>, ...struct fields}.
type ServerEvent interface {
	serverEventType() string
}

type TextDeltaEvent struct {
	Text string `json:"text"`
}

func (TextDeltaEvent) serverEventType() string { return "text_delta" }

type ReasoningDeltaEvent struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func (ReasoningDeltaEvent) serverEventType() string { return "reasoning_delta" }

type ToolCallEvent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
	Hint string `json:"hint,omitempty"`
}

func (ToolCallEvent) serverEventType() string { return "tool_call" }

type ToolResultEvent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (ToolResultEvent) serverEventType() string { return "tool_result" }

type PhaseEvent struct {
	Phase string `json:"phase"`
	Hint  string `json:"hint,omitempty"`
}

func (PhaseEvent) serverEventType() string { return "phase" }

type PromptEvent struct {
	Question string `json:"question"`
}

func (PromptEvent) serverEventType() string { return "prompt" }

type AskEvent struct {
	Question string `json:"question"`
}

func (AskEvent) serverEventType() string { return "ask" }

type ErrorEvent struct {
	Message string `json:"message"`
}

func (ErrorEvent) serverEventType() string { return "error" }

type DoneEvent struct{}

func (DoneEvent) serverEventType() string { return "done" }

type UsageEvent struct {
	InputTokens  int64 `json:"input_tokens"`
	CachedTokens int64 `json:"cached_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func (UsageEvent) serverEventType() string { return "usage" }

type MessagesEvent struct {
	Messages []ConversationMessage `json:"messages"`
}

func (MessagesEvent) serverEventType() string { return "messages" }

type SessionEvent struct {
	ID string `json:"id"`
}

func (SessionEvent) serverEventType() string { return "session" }

type DiffsChangedEvent struct{}

func (DiffsChangedEvent) serverEventType() string { return "diffs_changed" }

type CheckpointsChangedEvent struct{}

func (CheckpointsChangedEvent) serverEventType() string { return "checkpoints_changed" }

type SessionsChangedEvent struct{}

func (SessionsChangedEvent) serverEventType() string { return "sessions_changed" }

type FilesChangedEvent struct{}

func (FilesChangedEvent) serverEventType() string { return "files_changed" }

type DiagnosticsChangedEvent struct{}

func (DiagnosticsChangedEvent) serverEventType() string { return "diagnostics_changed" }

type CapabilitiesChangedEvent struct{}

func (CapabilitiesChangedEvent) serverEventType() string { return "capabilities_changed" }

// ConversationMessage is a simplified message for the REST /api/messages endpoint and WebSocket messages payload.
type ConversationMessage struct {
	Role    string                `json:"role"`
	Content []ConversationContent `json:"content"`
}

type ConversationContent struct {
	Text       string                 `json:"text,omitempty"`
	Reasoning  *ConversationReasoning `json:"reasoning,omitempty"`
	ToolCall   *ConversationTool      `json:"tool_call,omitempty"`
	ToolResult *ConversationResult    `json:"tool_result,omitempty"`
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
	Name    string `json:"name"`
	Args    string `json:"args,omitempty"`
	Content string `json:"content"`
}

// FileEntry represents a file or directory in the file browser.
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// FileContent represents the content of a file.
type FileContent struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Language string `json:"language"`
}

// DiffEntry represents a file diff from baseline.
type DiffEntry struct {
	Path     string `json:"path"`
	Status   string `json:"status"` // "added", "modified", "deleted"
	Patch    string `json:"patch"`
	Original string `json:"original,omitempty"`
	Modified string `json:"modified,omitempty"`
	Language string `json:"language,omitempty"`
}

// CheckpointEntry represents a single rewind checkpoint.
type CheckpointEntry struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

// SessionEntry represents a saved chat session in the sidebar list.
type SessionEntry struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
