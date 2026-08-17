package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/adrianliechti/wingman-agent/pkg/model"
)

// GenerateOptions describes a stateless, tool-free model request. It is used
// by latency-sensitive helpers that must not inherit a chat session, tools, or
// conversation history.
type GenerateOptions struct {
	Model           string
	Effort          string
	Instructions    string
	Input           string
	OutputSchema    map[string]any
	MaxOutputTokens int64
}

// GenerateResult is the visible response plus provider-reported usage for
// independent budgeting and accounting.
type GenerateResult struct {
	Text  string
	Usage Usage
}

// Generate runs one stateless Responses API request. A non-nil OutputSchema
// requests strict JSON schema output; callers remain responsible for decoding
// and semantically validating that JSON.
func (c *Config) Generate(ctx context.Context, opts GenerateOptions) (GenerateResult, error) {
	modelID := strings.TrimSpace(opts.Model)
	if modelID == "" {
		modelID = c.utilityModelName()
	}
	params := responses.ResponseNewParams{
		Model:        modelID,
		Instructions: openai.String(opts.Instructions),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(opts.Input),
		},
		Store:      openai.Bool(false),
		Truncation: responses.ResponseNewParamsTruncationDisabled,
	}
	if opts.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openai.Int(opts.MaxOutputTokens)
	}
	if opts.OutputSchema != nil {
		format := responses.ResponseFormatTextConfigParamOfJSONSchema(
			"response",
			opts.OutputSchema,
		)
		format.OfJSONSchema.Strict = openai.Bool(true)
		params.Text.Format = format
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		profile := model.ProfileFor(modelID)
		if profile.ReasoningEffortPlacement == model.ReasoningEffortAtRoot {
			params.SetExtraFields(map[string]any{"reasoning_effort": effort})
		} else {
			params.Reasoning.Effort = shared.ReasoningEffort(effort)
		}
	}

	resp, err := c.client.Responses.New(ctx, params)
	if err != nil {
		return GenerateResult{}, err
	}
	text := strings.TrimSpace(recoverySummaryOutput(resp))
	if opts.OutputSchema != nil && text != "" {
		var value any
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			return GenerateResult{}, fmt.Errorf("decode structured model response: %w", err)
		}
	}
	return GenerateResult{Text: text, Usage: responseToUsage(*resp)}, nil
}
