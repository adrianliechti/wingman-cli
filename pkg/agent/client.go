package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// streamIdleTimeout bounds the wait for the next stream event; reasoning
// models can be quiet for minutes between items, so it is generous.
const streamIdleTimeout = 5 * time.Minute

// streamFailure records whether a streamed request produced visible output
// before its transport failed. The response is not committed and tool calls
// are not executed until a terminal event arrives, so retrying remains safe;
// outputStarted only explains why a client may have seen repeated deltas.
type streamFailure struct {
	err           error
	outputStarted bool
}

func (e *streamFailure) Error() string { return e.err.Error() }
func (e *streamFailure) Unwrap() error { return e.err }

// responseFailure preserves the machine-readable code from a terminal
// Responses API error event. Some providers report transient failures in-band
// instead of as an HTTP error, so recovery needs more than the display string.
type responseFailure struct {
	code          string
	message       string
	outputStarted bool
}

func (e *responseFailure) Error() string {
	if e.message != "" {
		return fmt.Sprintf("response failed (%s): %s", e.code, e.message)
	}
	if e.code != "" {
		return fmt.Sprintf("response failed (%s)", e.code)
	}
	return "response failed"
}

type request struct {
	model        string
	effort       string
	instructions string
	cacheKey     string
	messages     []Message
	tools        []tool.Tool
	outputSchema map[string]any
}

type response struct {
	messages []Message
	usage    Usage

	incomplete       bool
	incompleteReason string
}

func complete(ctx context.Context, client *openai.Client, r *request, yield func(Message, error) bool) (*response, error) {
	params := responses.ResponseNewParams{
		Model:        r.model,
		Instructions: openai.String(r.instructions),

		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: toInput(r.messages)},

		Tools:             toTools(r.tools),
		ParallelToolCalls: openai.Bool(true),

		// Encrypted reasoning lets the model resume its chain of thought across
		// tool rounds instead of re-reasoning after every result.
		Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},

		Store: openai.Bool(false),
		// Overflow must surface as an error so compactMessages owns recovery;
		// "auto" would silently drop mid-conversation context server-side.
		Truncation: responses.ResponseNewParamsTruncationDisabled,
	}

	if r.cacheKey != "" {
		params.PromptCacheKey = openai.String(r.cacheKey)
	}

	if r.effort != "" {
		rp := responses.ReasoningParam{}

		rp.Effort = shared.ReasoningEffort(r.effort)

		// "auto" yields the richest summary the model supports; skipped for
		// "none" since no reasoning happens that could be summarized.
		if r.effort != "none" {
			rp.Summary = shared.ReasoningSummaryAuto
		}

		params.Reasoning = rp
	}

	if r.outputSchema != nil {
		if len(r.outputSchema) == 0 {
			format := shared.NewResponseFormatJSONObjectParam()
			params.Text.Format.OfJSONObject = &format
		} else {
			format := responses.ResponseFormatTextConfigParamOfJSONSchema(
				"response",
				r.outputSchema,
			)
			format.OfJSONSchema.Strict = openai.Bool(true)
			params.Text.Format = format
		}
	}

	// A stalled stream (no events at all) would otherwise hang the turn
	// forever; cancel it after a quiet period and let the retry loop resend.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	idle := time.AfterFunc(streamIdleTimeout, cancelStream)
	defer idle.Stop()

	stream := client.Responses.NewStreaming(streamCtx, params)

	var outputItems []responses.ResponseInputItemUnionParam
	var usageDelta Usage

	incomplete := false
	incompleteReason := ""
	outputStarted := false
	terminalEvent := false

	for stream.Next() {
		idle.Reset(streamIdleTimeout)
		event := stream.Current()
		// Error events are terminal metadata, not user-visible output. Preserve
		// the replay-safety state from before the event so a transient failure
		// can be retried when no output item or delta preceded it.
		switch event.Type {
		case "response.created", "response.in_progress", "response.queued", "response.failed", "error":
		default:
			outputStarted = true
		}

		switch e := event.AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			msg := Message{
				Role:    RoleAssistant,
				Content: []Content{{Text: e.Delta, TextID: e.ItemID}},
			}

			if !yield(msg, nil) {
				return nil, errYieldStopped
			}

		case responses.ResponseReasoningSummaryTextDeltaEvent:
			msg := Message{
				Role:    RoleAssistant,
				Content: []Content{{Reasoning: &Reasoning{ID: e.ItemID, Part: int(e.SummaryIndex), Summary: e.Delta}}},
			}

			if !yield(msg, nil) {
				return nil, errYieldStopped
			}

		case responses.ResponseOutputItemDoneEvent:
			switch item := e.Item.AsAny().(type) {
			case responses.ResponseOutputMessage:
				var p responses.ResponseOutputMessageParam
				if err := json.Unmarshal([]byte(item.RawJSON()), &p); err != nil {
					return nil, fmt.Errorf("failed to parse output message: %w", err)
				}

				outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
					OfOutputMessage: &p,
				})

			case responses.ResponseReasoningItem:
				var p responses.ResponseReasoningItemParam
				if err := json.Unmarshal([]byte(item.RawJSON()), &p); err != nil {
					return nil, fmt.Errorf("failed to parse reasoning item: %w", err)
				}

				outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
					OfReasoning: &p,
				})

			case responses.ResponseFunctionToolCall:
				var p responses.ResponseFunctionToolCallParam
				if err := json.Unmarshal([]byte(item.RawJSON()), &p); err != nil {
					return nil, fmt.Errorf("failed to parse function call: %w", err)
				}

				outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
					OfFunctionCall: &p,
				})
			}

		case responses.ResponseCompletedEvent:
			usageDelta = responseToUsage(e.Response)
			terminalEvent = true
			if items := outputItemsFromResponse(e.Response); len(items) >= len(outputItems) && len(items) > 0 {
				outputItems = items
			}

		case responses.ResponseIncompleteEvent:
			// Output was cut short (e.g. max output tokens). The final response
			// carries the partial items — including a message that never got an
			// output_item.done — so prefer it over the accumulated stream items,
			// or the resume round would regenerate everything already streamed.
			usageDelta = responseToUsage(e.Response)
			incomplete = true
			incompleteReason = e.Response.IncompleteDetails.Reason
			terminalEvent = true
			if items := outputItemsFromResponse(e.Response); len(items) > 0 {
				outputItems = items
			}

		case responses.ResponseFailedEvent:
			return nil, &responseFailure{
				code:          string(e.Response.Error.Code),
				message:       e.Response.Error.Message,
				outputStarted: outputStarted,
			}

		case responses.ResponseErrorEvent:
			return nil, &responseFailure{
				code:          e.Code,
				message:       e.Message,
				outputStarted: outputStarted,
			}
		}

		// completed and incomplete carry the authoritative final response.
		// Do not wait for a redundant [DONE] marker or connection close.
		if terminalEvent {
			break
		}
	}

	if err := stream.Err(); err != nil {
		if streamCtx.Err() != nil && ctx.Err() == nil {
			err = fmt.Errorf("stream stalled: no events for %s", streamIdleTimeout)
		}
		return nil, &streamFailure{err: err, outputStarted: outputStarted}
	}
	if !terminalEvent {
		return nil, &streamFailure{
			err:           io.ErrUnexpectedEOF,
			outputStarted: outputStarted,
		}
	}

	messages := toMessages(outputItems)

	for _, m := range messages {
		for _, c := range m.Content {
			if c.Reasoning != nil {
				c.Reasoning.Model = r.model
			}
		}
	}

	return &response{
		messages: messages,
		usage:    usageDelta,

		incomplete:       incomplete,
		incompleteReason: incompleteReason,
	}, nil
}

// outputItemsFromResponse converts a final Response's output into replayable
// input items. Function calls and reasoning are only usable when completed
// (truncated arguments or missing encrypted payloads are rejected on replay);
// partial message text is kept — preserving it is the point.
func outputItemsFromResponse(r responses.Response) []responses.ResponseInputItemUnionParam {
	var items []responses.ResponseInputItemUnionParam

	for _, item := range r.Output {
		switch it := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			var p responses.ResponseOutputMessageParam
			if err := json.Unmarshal([]byte(it.RawJSON()), &p); err == nil {
				items = append(items, responses.ResponseInputItemUnionParam{OfOutputMessage: &p})
			}

		case responses.ResponseReasoningItem:
			if it.Status != "completed" {
				continue
			}
			var p responses.ResponseReasoningItemParam
			if err := json.Unmarshal([]byte(it.RawJSON()), &p); err == nil {
				items = append(items, responses.ResponseInputItemUnionParam{OfReasoning: &p})
			}

		case responses.ResponseFunctionToolCall:
			if it.Status != "completed" {
				continue
			}
			var p responses.ResponseFunctionToolCallParam
			if err := json.Unmarshal([]byte(it.RawJSON()), &p); err == nil {
				items = append(items, responses.ResponseInputItemUnionParam{OfFunctionCall: &p})
			}
		}
	}

	return items
}
