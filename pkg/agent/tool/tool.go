package tool

import (
	"context"
)

type Effect string

const (
	EffectReadOnly  Effect = "read_only"
	EffectMutates   Effect = "mutates"
	EffectDangerous Effect = "dangerous"
	EffectDynamic   Effect = "dynamic"
)

func StaticEffect(effect Effect) func(map[string]any) Effect {
	return func(map[string]any) Effect {
		return effect
	}
}

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, args map[string]any) (string, error)
	Hidden      bool
	Effect      func(args map[string]any) Effect
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
