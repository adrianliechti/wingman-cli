package tool

import (
	"context"
	"fmt"
	"math"
	"time"
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

	// Timeout replaces the harness default tool timeout for this tool.
	// An explicitly configured Config.ToolTimeout takes precedence; negative
	// values disable the deadline.
	Timeout time.Duration
}

type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

type ElicitAction string

const (
	ElicitAccept  ElicitAction = "accept"
	ElicitDecline ElicitAction = "decline"
	ElicitCancel  ElicitAction = "cancel"
)

// ElicitField mirrors the MCP elicitation primitive schema: a flat, typed
// value request (string, number, integer, or boolean; optionally enum-
// constrained). EnumDescriptions extends the MCP shape with per-option help
// text; bridges to MCP drop it.
type ElicitField struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`

	Enum             []string `json:"enum,omitempty"`
	EnumDescriptions []string `json:"enum_descriptions,omitempty"`
	EnumPreviews     []string `json:"enum_previews,omitempty"`

	// Strict marks enum values as a closed set (MCP requestedSchema contract):
	// UIs must not offer a free-text alternative. The elicit tool's options
	// are advisory and stay non-strict.
	Strict bool `json:"strict,omitempty"`

	// Multiple allows selecting several enum values; the content value is then
	// a []string instead of a string. Never produced by the MCP bridge.
	Multiple bool `json:"multiple,omitempty"`

	// CustomAnswerFor marks this field as the free-text companion for another
	// enum field. UIs can present it as that field's "Other" choice instead of
	// asking an unconditional second question.
	CustomAnswerFor string `json:"custom_answer_for,omitempty"`

	Default any `json:"default,omitempty"`
}

type ElicitRequest struct {
	Message string        `json:"message"`
	Fields  []ElicitField `json:"fields,omitempty"`
}

type ElicitResult struct {
	Action  ElicitAction   `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

type Elicitation struct {
	Elicit  func(ctx context.Context, req ElicitRequest) (ElicitResult, error)
	Confirm func(ctx context.Context, message string) (bool, error)
}

func IntValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		if v > int64(math.MaxInt) || v < int64(math.MinInt) {
			return 0, false
		}
		return int(v), true
	case float64:
		if v > float64(math.MaxInt) || v < float64(math.MinInt) {
			return 0, false
		}
		if math.Trunc(v) != v {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

func IntArg(args map[string]any, key string) (int, bool) {
	return IntValue(args[key])
}

func OptionalIntArg(args map[string]any, key string) (int, bool, error) {
	raw, present := args[key]
	if !present {
		return 0, false, nil
	}

	value, ok := IntValue(raw)
	if !ok {
		return 0, true, fmt.Errorf("%s must be an integer", key)
	}

	return value, true, nil
}

func NonNegIntArg(args map[string]any, key string) (value int, present bool, err error) {
	raw, present := args[key]
	if !present {
		return 0, false, nil
	}

	v, ok := IntValue(raw)
	if !ok || v < 0 {
		return 0, true, fmt.Errorf("%s must be a non-negative integer", key)
	}

	return v, true, nil
}

func PositiveIntArg(args map[string]any, key string) (value int, present bool, err error) {
	raw, present := args[key]
	if !present {
		return 0, false, nil
	}

	v, ok := IntValue(raw)
	if !ok || v <= 0 {
		return 0, true, fmt.Errorf("%s must be a positive integer", key)
	}

	return v, true, nil
}
