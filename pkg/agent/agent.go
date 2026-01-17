package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

var errYieldStopped = errors.New("yield stopped")

type Agent struct {
	*config.Config

	messages        []responses.ResponseInputItemUnionParam
	lastInputTokens int64
}

type Message struct {
	Content []Content

	ToolCall   *ToolCall
	ToolResult *ToolResult

	Usage *Usage

	Compaction *CompactionInfo
}

type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

type Content struct {
	Text string
}

type ToolCall struct {
	ID   string
	Name string
	Args string
}

type ToolResult struct {
	ID      string
	Name    string
	Content []Content
}

func New(cfg *config.Config) *Agent {
	return &Agent{
		Config: cfg,
	}
}

func (a *Agent) Send(ctx context.Context, query string, tools []tool.Tool) iter.Seq2[Message, error] {
	a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(query)},
		},
	})

	return func(yield func(Message, error) bool) {
		formattedTools := formatTools(tools)

		for {
			text, toolCalls, usage, err := a.streamResponse(ctx, yield, formattedTools)

			if err != nil {
				if err != errYieldStopped {
					yield(Message{}, err)
				}

				return
			}

			if len(toolCalls) > 0 {
				if err := a.processToolCalls(yield, toolCalls, tools); err != nil {
					if err != errYieldStopped {
						yield(Message{}, err)
					}

					return
				}

				continue
			}

			if err := a.finalizeResponse(ctx, yield, text, usage); err != nil {
				if err != errYieldStopped {
					yield(Message{}, err)
				}
			}

			return
		}
	}
}

func (a *Agent) streamResponse(ctx context.Context, yield func(Message, error) bool, tools []responses.ToolUnionParam) (string, []responses.ResponseFunctionToolCall, Usage, error) {
	stream := a.Client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model:        a.Model,
		Instructions: openai.String(a.Instructions),
		Input:        responses.ResponseNewParamsInputUnion{OfInputItemList: a.messages},
		Tools:        tools,
		Truncation:   responses.ResponseNewParamsTruncationAuto,
	})

	var fullResponse strings.Builder
	var toolCalls []responses.ResponseFunctionToolCall
	var usage Usage

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "response.output_text.delta":
			fullResponse.WriteString(event.Delta)

			msg := Message{Content: []Content{{Text: event.Delta}}}

			if !yield(msg, nil) {
				return "", nil, Usage{}, errYieldStopped
			}

		case "response.output_item.done":
			if event.Item.Type == "function_call" {
				toolCalls = append(toolCalls, event.Item.AsFunctionCall())
			}
		}

		if event.Response.Usage.InputTokens > 0 {
			usage.InputTokens = event.Response.Usage.InputTokens
			usage.OutputTokens = event.Response.Usage.OutputTokens
			a.lastInputTokens = event.Response.Usage.InputTokens
		}
	}

	if err := stream.Err(); err != nil {
		return "", nil, Usage{}, err
	}

	return fullResponse.String(), toolCalls, usage, nil
}

func (a *Agent) processToolCalls(yield func(Message, error) bool, toolCalls []responses.ResponseFunctionToolCall, tools []tool.Tool) error {
	for _, tc := range toolCalls {
		a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
			OfFunctionCall: &responses.ResponseFunctionToolCallParam{
				CallID:    tc.CallID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			},
		})

		msg := Message{ToolCall: &ToolCall{ID: tc.CallID, Name: tc.Name, Args: tc.Arguments}}

		if !yield(msg, nil) {
			return errYieldStopped
		}

		result := a.executeTool(tc, tools)

		resultMsg := Message{ToolResult: &ToolResult{
			ID:      tc.CallID,
			Name:    tc.Name,
			Content: []Content{{Text: result}},
		}}

		if !yield(resultMsg, nil) {
			return errYieldStopped
		}

		a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
			OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
				CallID: tc.CallID,
				Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
					OfString: openai.String(result),
				},
			},
		})
	}

	return nil
}

func (a *Agent) finalizeResponse(ctx context.Context, yield func(Message, error) bool, text string, usage Usage) error {
	if text != "" {
		a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleAssistant,
				Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(text)},
			},
		})
	}

	if usage.InputTokens > 0 {
		if !yield(Message{Usage: &usage}, nil) {
			return errYieldStopped
		}
	}

	if !a.shouldCompact(a.lastInputTokens) {
		return nil
	}

	if !yield(Message{Compaction: &CompactionInfo{InProgress: true, FromTokens: a.lastInputTokens}}, nil) {
		return errYieldStopped
	}

	compaction, err := a.compact(ctx, a.lastInputTokens)

	if err != nil {
		return err
	}

	if compaction != nil {
		if !yield(Message{Compaction: compaction}, nil) {
			return errYieldStopped
		}
	}

	return nil
}

func formatTools(tools []tool.Tool) []responses.ToolUnionParam {
	var result []responses.ToolUnionParam

	for _, t := range tools {
		result = append(result, responses.ToolParamOfFunction(t.Name, t.Parameters, false))
	}

	return result
}

func (a *Agent) executeTool(tc responses.ResponseFunctionToolCall, tools []tool.Tool) string {
	var t *tool.Tool

	for i := range tools {
		if tools[i].Name == tc.Name {
			t = &tools[i]

			break
		}
	}

	if t == nil {
		return fmt.Sprintf("error: unknown tool %s", tc.Name)
	}

	var args map[string]any

	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		return fmt.Sprintf("error: failed to parse arguments: %v", err)
	}

	result, err := t.Execute(a.Environment, args)

	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	return result
}

func (a *Agent) Messages() []responses.ResponseInputItemUnionParam {
	return a.messages
}

func (a *Agent) Clear() {
	a.messages = nil
}
