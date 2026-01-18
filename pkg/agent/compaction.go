package agent

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-cli/pkg/prompt"
)

type CompactionInfo struct {
	InProgress bool
	FromTokens int64
	ToTokens   int64
}

func (a *Agent) shouldCompact(inputTokens int64) bool {
	if a.MaxContextTokens <= 0 {
		return false
	}

	return inputTokens > a.MaxContextTokens-a.ReserveTokens
}

func (a *Agent) compact(ctx context.Context, inputTokens int64) (*CompactionInfo, error) {
	cutIdx := a.findCutPoint()

	if cutIdx <= 0 {
		return nil, nil
	}

	toSummarize := a.messages[:cutIdx]
	toKeep := a.messages[cutIdx:]

	summary, err := a.summarize(ctx, toSummarize)

	if err != nil {
		return nil, err
	}

	summaryMsg := responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(summary)},
		},
	}

	a.messages = append([]responses.ResponseInputItemUnionParam{summaryMsg}, toKeep...)

	newTokens := a.estimateTokens()

	return &CompactionInfo{
		FromTokens: inputTokens,
		ToTokens:   newTokens,
	}, nil
}

func (a *Agent) findCutPoint() int {
	if len(a.messages) < 2 {
		return 0
	}

	var accumulated int64
	cutIdx := len(a.messages)

	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := a.messages[i]
		accumulated += estimateMessageTokens(msg)

		if accumulated >= a.KeepRecentTokens {
			cutIdx = i

			break
		}
	}

	cutIdx = a.adjustCutPoint(cutIdx)

	if cutIdx <= 0 || cutIdx >= len(a.messages) {
		return 0
	}

	return cutIdx
}

func (a *Agent) adjustCutPoint(idx int) int {
	for i := idx; i < len(a.messages); i++ {
		msg := a.messages[i]

		if msg.OfFunctionCallOutput != nil {
			continue
		}

		if msg.OfFunctionCall != nil {
			continue
		}

		return i
	}

	return len(a.messages)
}

func (a *Agent) estimateTokens() int64 {
	var total int64

	for _, msg := range a.messages {
		total += estimateMessageTokens(msg)
	}

	return total
}

func estimateMessageTokens(msg responses.ResponseInputItemUnionParam) int64 {
	var text string

	if msg.OfMessage != nil {
		if msg.OfMessage.Content.OfString.Valid() {
			text = msg.OfMessage.Content.OfString.Value
		}
	}

	if msg.OfFunctionCall != nil {
		text = msg.OfFunctionCall.Name + msg.OfFunctionCall.Arguments
	}

	if msg.OfFunctionCallOutput != nil {
		if msg.OfFunctionCallOutput.Output.OfString.Valid() {
			text = msg.OfFunctionCallOutput.Output.OfString.Value
		}
	}

	return int64(len(text) / 4)
}

func (a *Agent) summarize(ctx context.Context, messages []responses.ResponseInputItemUnionParam) (string, error) {
	var conversation strings.Builder

	for _, msg := range messages {
		if msg.OfMessage != nil {
			role := string(msg.OfMessage.Role)

			if msg.OfMessage.Content.OfString.Valid() {
				conversation.WriteString(role + ": " + msg.OfMessage.Content.OfString.Value + "\n\n")
			}
		}

		if msg.OfFunctionCall != nil {
			conversation.WriteString("tool_call: " + msg.OfFunctionCall.Name + "(" + msg.OfFunctionCall.Arguments + ")\n\n")
		}

		if msg.OfFunctionCallOutput != nil {
			if msg.OfFunctionCallOutput.Output.OfString.Valid() {
				output := msg.OfFunctionCallOutput.Output.OfString.Value

				if len(output) > 2000 {
					output = output[:2000] + "...[truncated]"
				}

				conversation.WriteString("tool_result: " + output + "\n\n")
			}
		}
	}

	resp, err := a.Client.Responses.New(ctx, responses.ResponseNewParams{
		Model: a.Model,

		Instructions: openai.String(prompt.Compaction),

		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(conversation.String()),
		},
	})

	if err != nil {
		return "", err
	}

	return resp.OutputText(), nil
}
