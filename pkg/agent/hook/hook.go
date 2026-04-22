package hook

import (
	"context"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// PreToolUse is called before a tool executes.
// Return a non-empty result to skip execution and use that result.
// Return an error to abort with that error as the result.
// Return ("", nil) to proceed normally.
type PreToolUse func(ctx context.Context, call tool.ToolCall) (string, error)

// PostToolUse is called after a tool executes.
// Receives the call and result. Return a modified result to transform it,
// or return the same result to pass through.
type PostToolUse func(ctx context.Context, call tool.ToolCall, result string) (string, error)

// Hooks holds the registered hook functions for an agent.
type Hooks struct {
	PreToolUse  []PreToolUse
	PostToolUse []PostToolUse
}
