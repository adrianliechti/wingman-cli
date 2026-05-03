package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type request struct {
	model        string
	effort       string
	instructions string
	messages     []Message
	tools        []tool.Tool
}

type response struct {
	messages []Message
	usage    Usage
}

func complete(ctx context.Context, client *openai.Client, r *request, yield func(Message, error) bool) (*response, error) {
	stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model:        r.model,
		Instructions: openai.String(r.instructions),

		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: toInput(r.messages)},

		Tools:             toTools(r.tools),
		ParallelToolCalls: openai.Bool(true),

		Store:      openai.Bool(false),
		Truncation: responses.ResponseNewParamsTruncationAuto,

		ContextManagement: []responses.ResponseNewParamsContextManagement{{
			Type:             "compaction",
			CompactThreshold: openai.Int(200000),
		}},

		Include: []responses.ResponseIncludable{
			responses.ResponseIncludableReasoningEncryptedContent,
		},

		Reasoning: responses.ReasoningParam{
			Summary: responses.ReasoningSummaryAuto,
			Effort:  shared.ReasoningEffort(r.effort),
		},
	})

	var outputItems []responses.ResponseInputItemUnionParam
	var usageDelta Usage

	for stream.Next() {
		event := stream.Current()

		switch e := event.AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			msg := Message{
				Role:    RoleAssistant,
				Content: []Content{{Text: e.Delta}},
			}

			if !yield(msg, nil) {
				return nil, errYieldStopped
			}

		case responses.ResponseReasoningSummaryTextDeltaEvent:
			msg := Message{
				Role:    RoleAssistant,
				Content: []Content{{Reasoning: &Reasoning{ID: e.ItemID, Summary: e.Delta}}},
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

			case responses.ResponseCompactionItem:
				outputItems = append(outputItems, compactionEventToInput(item))
			}

		case responses.ResponseCompletedEvent:
			usageDelta = responseToUsage(e.Response)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	return &response{
		messages: toMessages(outputItems),
		usage:    usageDelta,
	}, nil
}
