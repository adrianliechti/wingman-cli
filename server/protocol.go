package server

import (
	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

const (
	EvtTextDelta           = "text_delta"
	EvtReasoningDelta      = "reasoning_delta"
	EvtToolCall            = "tool_call"
	EvtToolResult          = "tool_result"
	EvtToolProgress        = "tool_progress"
	EvtStreamReset         = "stream_reset"
	EvtPhase               = "phase"
	EvtError               = "error"
	EvtFilesChanged        = "files_changed"
	EvtDiffsChanged        = "diffs_changed"
	EvtGitIndexChanged     = "git_index_changed"
	EvtSessionsChanged     = "sessions_changed"
	EvtDiagnosticsChanged  = "diagnostics_changed"
	EvtCapabilitiesChanged = "capabilities_changed"
	EvtModelChanged        = "model_changed"
	EvtTurnInput           = "turn_input"
	EvtTasksChanged        = "tasks_changed"
	EvtTerminalsChanged    = "terminals_changed"
	EvtSkillsChanged       = "skills_changed"
)

const (
	PromptKindAsk      = "ask"
	PromptKindConfirm  = "confirm"
	PromptScopeSession = "session"
)

// Frame carries internal adapter updates and workspace resource invalidations.
// Session frames are projected by sessionController; only sessionEvent is public.
type Frame struct {
	Type    string `json:"type"`
	Session string `json:"session,omitempty"`
	Backend string `json:"backend,omitempty"`

	Text      string               `json:"text,omitempty"`
	ID        string               `json:"id,omitempty"`
	Name      string               `json:"name,omitempty"`
	Kind      string               `json:"kind,omitempty"`
	Args      string               `json:"args,omitempty"`
	Locations []agent.ToolLocation `json:"locations,omitempty"`
	Hint      string               `json:"hint,omitempty"`
	Content   string               `json:"content,omitempty"`
	Phase     string               `json:"phase,omitempty"`
	Message   string               `json:"message,omitempty"`
	Part      int                  `json:"part,omitempty"`
	Partial   bool                 `json:"partial,omitempty"`
	Input     *TurnQueueEntry      `json:"input,omitempty"`
}

type TurnQueueEntry struct {
	Origin   string   `json:"origin,omitempty"`
	ID       string   `json:"id"`
	State    string   `json:"state"`
	Intent   string   `json:"intent"`
	Position int      `json:"position,omitempty"`
	Text     string   `json:"text,omitempty"`
	Files    []string `json:"files,omitempty"`
	Images   []string `json:"images,omitempty"`
}

type ConversationMessage struct {
	InputID string                `json:"input_id,omitempty"`
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
	ID        string               `json:"id,omitempty"`
	Name      string               `json:"name"`
	Kind      string               `json:"kind,omitempty"`
	Args      string               `json:"args,omitempty"`
	Locations []agent.ToolLocation `json:"locations,omitempty"`
	Hint      string               `json:"hint,omitempty"`
}

type ConversationResult struct {
	ID        string               `json:"id,omitempty"`
	Name      string               `json:"name"`
	Kind      string               `json:"kind,omitempty"`
	Args      string               `json:"args,omitempty"`
	Locations []agent.ToolLocation `json:"locations,omitempty"`
	Content   string               `json:"content"`
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
	UpdatedAt string `json:"updated_at"`
}
