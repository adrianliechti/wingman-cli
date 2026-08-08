package hook

import (
	"context"
	"encoding/json"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// Runtime carries the Codex hook fields shared by lifecycle events. Callers
// may populate only the fields they know; the external hook adapter supplies
// Codex-compatible zero/null values for the rest.
type Runtime struct {
	SessionID      string
	TurnID         string
	TranscriptPath string
	CWD            string
	Model          string
	PermissionMode string
	StartSource    string
	AgentID        string
	AgentType      string
}

type runtimeKey struct{}
type toolCallKey struct{}

func WithRuntime(ctx context.Context, runtime Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, runtime)
}

func RuntimeFromContext(ctx context.Context) Runtime {
	runtime, _ := ctx.Value(runtimeKey{}).(Runtime)
	return runtime
}

// WithToolCall makes the active call available to permission hooks that fire
// from inside a tool while it asks the host for approval.
func WithToolCall(ctx context.Context, call tool.ToolCall) context.Context {
	return context.WithValue(ctx, toolCallKey{}, call)
}

func ToolCallFromContext(ctx context.Context) (tool.ToolCall, bool) {
	call, ok := ctx.Value(toolCallKey{}).(tool.ToolCall)
	return call, ok
}

type Outcome struct {
	// Block has event-specific Codex semantics: deny a tool/prompt, stop the
	// agentic loop, or continue a Stop/SubagentStop target.
	Block bool
	// Stop represents the universal {"continue": false} response.
	Stop              bool
	Reason            string
	AdditionalContext []string
	// UpdatedResult is an internal extension used by deterministic result
	// transforms such as truncation. Codex command hooks leave it nil.
	UpdatedResult *string
}

type PreToolUseOutcome struct {
	Outcome
	UpdatedInput json.RawMessage
}

type PermissionBehavior string

const (
	PermissionUndecided PermissionBehavior = ""
	PermissionAllow     PermissionBehavior = "allow"
	PermissionDeny      PermissionBehavior = "deny"
)

type PermissionRequestOutcome struct {
	Behavior PermissionBehavior
	Message  string
}

type PreToolUse func(ctx context.Context, call tool.ToolCall) (PreToolUseOutcome, error)
type PermissionRequest func(ctx context.Context, call tool.ToolCall) (PermissionRequestOutcome, error)
type PostToolUse func(ctx context.Context, call tool.ToolCall, result string) (Outcome, error)
type UserPromptSubmit func(ctx context.Context, prompt string) (Outcome, error)
type SessionStart func(ctx context.Context, source string) (Outcome, error)
type SessionEnd func(ctx context.Context, reason string)
type SubagentStart func(ctx context.Context, agentID, agentType string) (Outcome, error)
type SubagentStop func(ctx context.Context, agentID, agentType, result string, active bool) (Outcome, error)
type PreCompact func(ctx context.Context, trigger string) (Outcome, error)
type PostCompact func(ctx context.Context, trigger string) (Outcome, error)
type Stop func(ctx context.Context, lastAssistantMessage string, active bool) (Outcome, error)

type Hooks struct {
	PreToolUse        []PreToolUse
	PermissionRequest []PermissionRequest
	PostToolUse       []PostToolUse
	UserPromptSubmit  []UserPromptSubmit
	SessionStart      []SessionStart
	SessionEnd        []SessionEnd
	SubagentStart     []SubagentStart
	SubagentStop      []SubagentStop
	PreCompact        []PreCompact
	PostCompact       []PostCompact
	Stop              []Stop
}

func (h *Hooks) Append(other Hooks) {
	h.PreToolUse = append(h.PreToolUse, other.PreToolUse...)
	h.PermissionRequest = append(h.PermissionRequest, other.PermissionRequest...)
	h.PostToolUse = append(h.PostToolUse, other.PostToolUse...)
	h.UserPromptSubmit = append(h.UserPromptSubmit, other.UserPromptSubmit...)
	h.SessionStart = append(h.SessionStart, other.SessionStart...)
	h.SessionEnd = append(h.SessionEnd, other.SessionEnd...)
	h.SubagentStart = append(h.SubagentStart, other.SubagentStart...)
	h.SubagentStop = append(h.SubagentStop, other.SubagentStop...)
	h.PreCompact = append(h.PreCompact, other.PreCompact...)
	h.PostCompact = append(h.PostCompact, other.PostCompact...)
	h.Stop = append(h.Stop, other.Stop...)
}
