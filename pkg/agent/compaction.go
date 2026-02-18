package agent

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-cli/pkg/prompt"
)

func (a *Agent) shouldCompact(inputTokens int64) bool {
	if a.MaxContextTokens <= 0 {
		return false
	}

	return inputTokens > a.MaxContextTokens-a.ReserveTokens
}

func (a *Agent) compact(ctx context.Context) {
	cutIdx := a.findCutPoint()

	if cutIdx <= 0 {
		return
	}

	toSummarize := a.messages[:cutIdx]
	toKeep := a.messages[cutIdx:]

	summary, err := a.summarize(ctx, toSummarize)

	if err != nil {
		// Graceful fallback: if summarization fails, just continue without compacting.
		// The server-side Truncation: Auto safety net will handle overflow.
		return
	}

	summaryMsg := responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(summary)},
		},
	}

	a.messages = append([]responses.ResponseInputItemUnionParam{summaryMsg}, toKeep...)
	a.usage.InputTokens = a.estimateTokens()
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

const maxSummaryContentLength = 3000

func (a *Agent) summarize(ctx context.Context, messages []responses.ResponseInputItemUnionParam) (string, error) {
	var conversation strings.Builder

	for _, msg := range messages {
		if msg.OfMessage != nil {
			if msg.OfMessage.Content.OfString.Valid() {
				content := msg.OfMessage.Content.OfString.Value
				role := string(msg.OfMessage.Role)

				conversation.WriteString("[" + role + "]\n" + content + "\n\n")
			}
		}

		if msg.OfFunctionCall != nil {
			args := msg.OfFunctionCall.Arguments

			if len(args) > maxSummaryContentLength {
				args = args[:maxSummaryContentLength] + "...[truncated]"
			}

			conversation.WriteString("[Tool Call] " + msg.OfFunctionCall.Name + "\n" + args + "\n\n")
		}

		if msg.OfFunctionCallOutput != nil {
			if msg.OfFunctionCallOutput.Output.OfString.Valid() {
				output := msg.OfFunctionCallOutput.Output.OfString.Value

				if len(output) > maxSummaryContentLength {
					output = output[:maxSummaryContentLength] + "...[truncated]"
				}

				conversation.WriteString("[Tool Result]\n" + output + "\n\n")
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
