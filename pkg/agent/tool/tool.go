package tool

import (
	"context"
)

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, args map[string]any) (string, error)
	Hidden      bool
}

// ToolCall describes a pending tool invocation.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

// Elicitation allows tools to request information from the user.
type Elicitation struct {
	Ask     func(ctx context.Context, message string) (string, error)
	Confirm func(ctx context.Context, message string) (bool, error)
}
