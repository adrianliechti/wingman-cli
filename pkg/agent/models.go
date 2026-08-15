package agent

type ModelInfo struct {
	ID string
}

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

type Message struct {
	Role MessageRole `json:"role"`

	Content []Content `json:"content"`
	Hidden  bool      `json:"hidden,omitempty"`
}

type Content struct {
	Text string `json:"text,omitempty"`
	// TextID is source-local identity used to reconcile streamed and retained
	// UI content. It is metadata, not a provider payload to replay in requests.
	TextID string `json:"text_id,omitempty"`

	Refusal string `json:"refusal,omitempty"`

	// Hidden marks injected context (e.g. background-task notifications) that
	// the model must see but UIs must not render as user input. A user message
	// whose content is entirely hidden becomes a hidden message.
	Hidden bool `json:"hidden,omitempty"`

	File *File `json:"file,omitempty"`

	Reasoning *Reasoning `json:"reasoning,omitempty"`

	ToolCall   *ToolCall   `json:"tool_call,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
}

type File struct {
	Name string `json:"name,omitempty"`
	Data string `json:"data,omitempty"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	CachedTokens int64 `json:"cached_tokens"`
	OutputTokens int64 `json:"output_tokens"`

	// LastInputTokens is the input size of the most recent request — the
	// current context occupancy, unlike the cumulative counters above.
	LastInputTokens int64 `json:"last_input_tokens,omitempty"`
	// ContextWindow is the provider-reported maximum context size when the
	// transport supplies it directly.
	ContextWindow int64 `json:"context_window,omitempty"`
}

type ToolCall struct {
	ID string `json:"id"`

	Name string `json:"name"`
	Args string `json:"args,omitempty"`

	Partial bool `json:"partial,omitempty"`
}

type ToolResult struct {
	ID string `json:"id,omitempty"`

	Name string `json:"name"`
	Args string `json:"args,omitempty"`

	Content  string         `json:"content,omitempty"`
	IsError  bool           `json:"is_error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Reasoning struct {
	ID string `json:"id,omitempty"`

	Summary string `json:"summary,omitempty"`

	// Part indexes the summary part a streamed delta belongs to; renderers
	// separate parts however suits their medium.
	Part int `json:"part,omitempty"`

	// Content is the provider's opaque (encrypted) reasoning payload, only
	// replayable to the model that produced it. Model tags the producer so the
	// agent loop can purge stale payloads when the session model changes.
	Content string `json:"content,omitempty"`
	Model   string `json:"model,omitempty"`
}
