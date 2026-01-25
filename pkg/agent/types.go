package agent

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

type Message struct {
	Role MessageRole

	Content []Content
}

type Content struct {
	Text string

	File *File

	ToolCall   *ToolCall
	ToolResult *ToolResult
}

type File struct {
	Name string
	Data string // base64 data URL, e.g. "data:image/png;base64,..."
}

type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

type ToolCall struct {
	ID string

	Name string
	Args string
}

type ToolResult struct {
	ID string

	Name string
	Args string

	Content string
}
