package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"go.opentelemetry.io/otel/attribute"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/telemetry"
)

func conversationID(ctx context.Context, fallback string) string {
	if id := hook.RuntimeFromContext(ctx).SessionID; id != "" {
		return id
	}
	return fallback
}

func telemetryOutcome(err error) telemetry.Outcome {
	outcome := telemetry.Outcome{Err: err}
	if err == nil {
		return outcome
	}
	if errors.Is(err, errYieldStopped) {
		outcome.ErrorType = "canceled"
		return outcome
	}
	if responseErr, ok := errors.AsType[*responseFailure](err); ok && responseErr.code != "" {
		outcome.ErrorType = responseErr.code
		return outcome
	}
	if apiErr, ok := errors.AsType[*openai.Error](err); ok {
		switch {
		case apiErr.Code != "":
			outcome.ErrorType = apiErr.Code
		case apiErr.StatusCode > 0:
			outcome.ErrorType = strconv.Itoa(apiErr.StatusCode)
		}
	}
	return outcome
}

func inferenceResult(resp *responses.Response, usage Usage, err error, captureContent bool) telemetry.InferenceResult {
	result := telemetry.InferenceResult{Outcome: telemetryOutcome(err)}
	if resp == nil {
		return result
	}
	messages := toMessages(outputItemsFromResponse(*resp))
	result.ResponseID = resp.ID
	result.ResponseModel = resp.Model
	result.FinishReasons = telemetryFinishReasons(string(resp.Status), resp.IncompleteDetails.Reason, messages)
	result.Usage = telemetryTokenUsage(usage)
	if captureContent {
		result.OutputMessages = telemetryOutputMessages(messages, result.FinishReasons)
	}
	return result
}

func streamingInferenceResult(resp *response, err error, captureContent bool) telemetry.InferenceResult {
	result := telemetry.InferenceResult{Outcome: telemetryOutcome(err)}
	if resp == nil {
		return result
	}
	result.ResponseID = resp.id
	result.ResponseModel = resp.model
	result.FinishReasons = append([]string(nil), resp.finishReasons...)
	result.Usage = telemetryTokenUsage(resp.usage)
	if captureContent {
		result.OutputMessages = telemetryOutputMessages(resp.messages, resp.finishReasons)
	}
	return result
}

func telemetryTokenUsage(usage Usage) *telemetry.TokenUsage {
	return &telemetry.TokenUsage{
		InputTokens:           usage.InputTokens,
		CacheReadInputTokens:  usage.CacheReadInputTokens,
		CacheWriteInputTokens: usage.CacheCreationInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningTokens,
	}
}

func telemetryFinishReasons(status, incompleteReason string, messages []Message) []string {
	switch strings.TrimSpace(status) {
	case "failed", "cancelled":
		return []string{"error"}
	case "incomplete":
		reason := strings.TrimSpace(incompleteReason)
		switch reason {
		case "max_output_tokens":
			reason = "length"
		case "":
			reason = "error"
		}
		return []string{reason}
	case "queued", "in_progress":
		return nil
	}

	for _, message := range messages {
		for _, content := range message.Content {
			if content.ToolCall != nil {
				return []string{"tool_call"}
			}
		}
	}
	return []string{"stop"}
}

func telemetryInferenceContent(messages []Message, instructions string, tools []tool.Tool) telemetry.InferenceContent {
	return telemetry.InferenceContent{
		InputMessages:      telemetryInputMessages(messages),
		SystemInstructions: telemetrySystemInstructions(instructions),
		ToolDefinitions:    telemetryToolDefinitions(tools),
	}
}

func telemetryStringInput(input string) attribute.Value {
	if input == "" {
		return attribute.Value{}
	}
	return telemetryInputMessages([]Message{{
		Role:    RoleUser,
		Content: []Content{{Text: input}},
	}})
}

func telemetryInputMessages(messages []Message) attribute.Value {
	values := make([]attribute.Value, 0, len(messages))
	for _, message := range messages {
		parts, onlyToolResponses := telemetryMessageParts(message.Content)
		if len(parts) == 0 {
			continue
		}
		role := string(message.Role)
		if onlyToolResponses {
			role = "tool"
		}
		values = append(values, attribute.MapValue(
			attribute.String("role", role),
			attribute.Key("parts").Slice(parts...),
		))
	}
	if len(values) == 0 {
		return attribute.Value{}
	}
	return attribute.SliceValue(values...)
}

func telemetryOutputMessages(messages []Message, finishReasons []string) attribute.Value {
	var parts []attribute.Value
	for _, message := range messages {
		messageParts, _ := telemetryMessageParts(message.Content)
		parts = append(parts, messageParts...)
	}
	if len(parts) == 0 {
		return attribute.Value{}
	}
	finishReason := ""
	if len(finishReasons) > 0 {
		finishReason = finishReasons[0]
	}
	attrs := []attribute.KeyValue{
		attribute.String("role", "assistant"),
		attribute.Key("parts").Slice(parts...),
	}
	if finishReason != "" {
		attrs = append(attrs, attribute.String("finish_reason", finishReason))
	}
	return attribute.SliceValue(attribute.MapValue(attrs...))
}

func telemetryMessageParts(contents []Content) (parts []attribute.Value, onlyToolResponses bool) {
	onlyToolResponses = true
	for _, content := range contents {
		if content.Text != "" {
			parts = append(parts, telemetryTextPart(content.Text))
			onlyToolResponses = false
		}
		if content.Refusal != "" {
			parts = append(parts, telemetryTextPart(content.Refusal))
			onlyToolResponses = false
		}
		if content.ToolCall != nil {
			attrs := []attribute.KeyValue{
				attribute.String("type", "tool_call"),
				attribute.String("id", content.ToolCall.ID),
				attribute.String("name", content.ToolCall.Name),
			}
			if arguments, ok := telemetryJSON(content.ToolCall.Args); ok {
				attrs = append(attrs, attribute.KeyValue{Key: "arguments", Value: arguments})
			}
			parts = append(parts, attribute.MapValue(attrs...))
			onlyToolResponses = false
		}
		if content.ToolResult != nil {
			attrs := []attribute.KeyValue{
				attribute.String("type", "tool_call_response"),
				attribute.String("id", content.ToolResult.ID),
			}
			if response, ok := telemetryJSONOrString(content.ToolResult.Content); ok {
				attrs = append(attrs, attribute.KeyValue{Key: "response", Value: response})
			}
			parts = append(parts, attribute.MapValue(attrs...))
		}
	}
	return parts, len(parts) > 0 && onlyToolResponses
}

func telemetryTextPart(content string) attribute.Value {
	return attribute.MapValue(
		attribute.String("type", "text"),
		attribute.String("content", content),
	)
}

func telemetrySystemInstructions(instructions string) attribute.Value {
	if instructions == "" {
		return attribute.Value{}
	}
	return attribute.SliceValue(telemetryTextPart(instructions))
}

func telemetryToolDefinitions(tools []tool.Tool) attribute.Value {
	values := make([]attribute.Value, 0, len(tools))
	for _, item := range tools {
		attrs := []attribute.KeyValue{
			attribute.String("type", "function"),
			attribute.String("name", item.Name),
		}
		if item.Description != "" {
			attrs = append(attrs, attribute.String("description", item.Description))
		}
		if parameters, ok := telemetry.StructuredValue(item.Parameters); ok {
			attrs = append(attrs, attribute.KeyValue{Key: "parameters", Value: parameters})
		}
		values = append(values, attribute.MapValue(attrs...))
	}
	if len(values) == 0 {
		return attribute.Value{}
	}
	return attribute.SliceValue(values...)
}

func telemetryJSON(value string) (attribute.Value, bool) {
	if strings.TrimSpace(value) == "" {
		return attribute.Value{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return attribute.Value{}, false
	}
	return telemetry.StructuredValue(decoded)
}

func telemetryJSONOrString(value string) (attribute.Value, bool) {
	if parsed, ok := telemetryJSON(value); ok {
		return parsed, true
	}
	return attribute.StringValue(value), true
}
