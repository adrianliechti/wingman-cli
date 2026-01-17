package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/tool"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

type Agent struct {
	*config.Config

	messages []responses.ResponseInputItemUnionParam
}

type Message struct {
	Content    []Content
	ToolCall   *ToolCall
	ToolResult *ToolResult
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
			var fullResponse strings.Builder
			var toolCalls []responses.ResponseFunctionToolCall

			stream := a.Client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
				Model: a.Model,

				Instructions: openai.String(a.Instructions),

				Input: responses.ResponseNewParamsInputUnion{
					OfInputItemList: a.messages,
				},

				Tools: formattedTools,
			})

			for stream.Next() {
				event := stream.Current()

				if event.Type == "response.output_text.delta" {
					fullResponse.WriteString(event.Delta)

					msg := Message{Content: []Content{{Text: event.Delta}}}
					if !yield(msg, nil) {
						return
					}
				}

				if event.Type == "response.output_item.done" && event.Item.Type == "function_call" {
					toolCalls = append(toolCalls, event.Item.AsFunctionCall())
				}
			}

			if err := stream.Err(); err != nil {
				yield(Message{}, err)

				return
			}

			if len(toolCalls) == 0 {
				if text := fullResponse.String(); text != "" {
					a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
						OfMessage: &responses.EasyInputMessageParam{
							Role:    responses.EasyInputMessageRoleAssistant,
							Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(text)},
						},
					})
				}

				return
			}

			for _, tc := range toolCalls {
				a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
					OfFunctionCall: &responses.ResponseFunctionToolCallParam{
						CallID:    tc.CallID,
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})

				toolCallMsg := Message{ToolCall: &ToolCall{
					ID:   tc.CallID,
					Name: tc.Name,
					Args: tc.Arguments,
				}}
				if !yield(toolCallMsg, nil) {
					return
				}

				result := a.executeTool(tc, tools)

				toolResultMsg := Message{ToolResult: &ToolResult{
					ID:      tc.CallID,
					Name:    tc.Name,
					Content: []Content{{Text: result}},
				}}
				if !yield(toolResultMsg, nil) {
					return
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
		}
	}
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
