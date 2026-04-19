package server

// Client -> Server message types
const (
	MsgSend           = "send"
	MsgCancel         = "cancel"
	MsgPromptResponse = "prompt_response"
	MsgAskResponse    = "ask_response"
)

// Server -> Client message types
const (
	MsgTextDelta  = "text_delta"
	MsgToolCall   = "tool_call"
	MsgToolResult = "tool_result"
	MsgPhase      = "phase"
	MsgPrompt     = "prompt"
	MsgAsk        = "ask"
	MsgError      = "error"
	MsgDone       = "done"
	MsgUsage      = "usage"
	MsgMessages   = "messages"
)

// ClientMessage is the envelope for all client-to-server WebSocket messages.
type ClientMessage struct {
	Type     string   `json:"type"`
	Text     string   `json:"text,omitempty"`
	Files    []string `json:"files,omitempty"`
	Approved bool     `json:"approved,omitempty"`
	Answer   string   `json:"answer,omitempty"`
}

// ServerMessage is the envelope for all server-to-client WebSocket messages.
type ServerMessage struct {
	Type string `json:"type"`

	// text_delta
	Text string `json:"text,omitempty"`

	// tool_call / tool_result
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Args    string `json:"args,omitempty"`
	Hint    string `json:"hint,omitempty"`
	Content string `json:"content,omitempty"`

	// phase
	Phase string `json:"phase,omitempty"`

	// prompt / ask
	Question string `json:"question,omitempty"`

	// error
	Message string `json:"message,omitempty"`

	// usage
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`

	// messages (full conversation)
	Messages []ConversationMessage `json:"messages,omitempty"`
}

// ConversationMessage is a simplified message for the REST /api/messages endpoint and WebSocket messages payload.
type ConversationMessage struct {
	Role    string                   `json:"role"`
	Content []ConversationContent    `json:"content"`
}

type ConversationContent struct {
	Text       string              `json:"text,omitempty"`
	ToolCall   *ConversationTool   `json:"tool_call,omitempty"`
	ToolResult *ConversationResult `json:"tool_result,omitempty"`
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
	Path   string `json:"path"`
	Status string `json:"status"` // "added", "modified", "deleted"
	Patch  string `json:"patch"`
}
