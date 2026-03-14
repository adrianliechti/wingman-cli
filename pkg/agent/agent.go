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

	messages []responses.ResponseInputItemUnionParam
	usage    Usage
}

func New(cfg *config.Config) *Agent {
	return &Agent{
		Config: cfg,
	}
}

func (a *Agent) Send(ctx context.Context, instructions string, input []Content, tools []tool.Tool) iter.Seq2[Message, error] {
	a.messages = append(a.messages, a.userMessage(input))

	return func(yield func(Message, error) bool) {
		formattedTools := formatTools(tools)

		text, toolCalls, usage, err := a.streamResponse(ctx, yield, instructions, formattedTools)

		for len(toolCalls) > 0 && err == nil {
			// Save any streamed text before processing tool calls
			if text != "" {
				a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role:    responses.EasyInputMessageRoleAssistant,
						Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(text)},
					},
				})
			}

			err = a.processToolCalls(ctx, yield, toolCalls, tools)

			if err != nil {
				break
			}

			text, toolCalls, usage, err = a.streamResponse(ctx, yield, instructions, formattedTools)
		}

		if err != nil {
			if err != errYieldStopped {
				yield(Message{}, err)
			}

			return
		}

		// Finalize response
		if text != "" {
			a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    responses.EasyInputMessageRoleAssistant,
					Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(text)},
				},
			})
		}

		if usage.InputTokens > 0 {
			a.usage.InputTokens = usage.InputTokens
			a.usage.OutputTokens += usage.OutputTokens
		}

		if a.shouldCompact(usage.InputTokens) {
			a.compact(ctx)
			a.usage.OutputTokens = 0
		}
	}
}

func (a *Agent) streamResponse(ctx context.Context, yield func(Message, error) bool, instructions string, tools []responses.ToolUnionParam) (string, []ToolCall, Usage, error) {
	stream := a.Client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model:        a.Model,
		Instructions: openai.String(instructions),
		Input:        responses.ResponseNewParamsInputUnion{OfInputItemList: a.messages},
		Tools:        tools,
		Truncation:   responses.ResponseNewParamsTruncationAuto,
	})

	var fullResponse strings.Builder
	var toolCalls []ToolCall
	var usage Usage

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "response.output_text.delta":
			fullResponse.WriteString(event.Delta)

			msg := Message{
				Role:    RoleAssistant,
				Content: []Content{{Text: event.Delta}},
			}

			if !yield(msg, nil) {
				return "", nil, Usage{}, errYieldStopped
			}

		case "response.output_item.done":
			if event.Item.Type == "function_call" {
				fc := event.Item.AsFunctionCall()
				toolCalls = append(toolCalls, ToolCall{
					ID:   fc.CallID,
					Name: fc.Name,
					Args: fc.Arguments,
				})
			}
		}

		if event.Response.Usage.InputTokens > 0 {
			usage.InputTokens = event.Response.Usage.InputTokens
			usage.OutputTokens = event.Response.Usage.OutputTokens
		}
	}

	if err := stream.Err(); err != nil {
		return "", nil, Usage{}, err
	}

	return fullResponse.String(), toolCalls, usage, nil
}

func (a *Agent) processToolCalls(ctx context.Context, yield func(Message, error) bool, toolCalls []ToolCall, tools []tool.Tool) error {
	for _, tc := range toolCalls {
		a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
			OfFunctionCall: &responses.ResponseFunctionToolCallParam{
				CallID:    tc.ID,
				Name:      tc.Name,
				Arguments: tc.Args,
			},
		})

		msg := Message{
			Role:    RoleAssistant,
			Content: []Content{{ToolCall: &ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args}}},
		}

		if !yield(msg, nil) {
			return errYieldStopped
		}

		result := a.executeTool(ctx, tc, tools)

		resultMsg := Message{
			Role: RoleAssistant,
			Content: []Content{{ToolResult: &ToolResult{
				ID:      tc.ID,
				Name:    tc.Name,
				Content: result,
			}}},
		}

		if !yield(resultMsg, nil) {
			return errYieldStopped
		}

		a.messages = append(a.messages, responses.ResponseInputItemUnionParam{
			OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
				CallID: tc.ID,
				Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
					OfString: openai.String(result),
				},
			},
		})
	}

	return nil
}

func formatTools(tools []tool.Tool) []responses.ToolUnionParam {
	var result []responses.ToolUnionParam

	for _, t := range tools {

		f := &responses.FunctionToolParam{
			Name:       t.Name,
			Parameters: t.Parameters,
			Strict:     openai.Bool(false),
		}

		if t.Description != "" {
			f.Description = openai.String(t.Description)
		}

		result = append(result, responses.ToolUnionParam{
			OfFunction: f,
		})
	}

	return result
}

func (a *Agent) executeTool(ctx context.Context, tc ToolCall, tools []tool.Tool) string {
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

	if err := json.Unmarshal([]byte(tc.Args), &args); err != nil {
		return fmt.Sprintf("error: failed to parse arguments: %v", err)
	}

	result, err := t.Execute(ctx, a.Environment, args)

	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	return result
}

func (a *Agent) userMessage(input []Content) responses.ResponseInputItemUnionParam {
	var parts responses.ResponseInputMessageContentListParam

	for _, c := range input {
		if c.Text != "" {
			parts = append(parts, responses.ResponseInputContentParamOfInputText(c.Text))
		}

		if c.File != nil && c.File.Data != "" {
			parts = append(parts, responses.ResponseInputContentUnionParam{
				OfInputImage: &responses.ResponseInputImageParam{
					ImageURL: openai.String(c.File.Data),
					Detail:   responses.ResponseInputImageDetailAuto,
				},
			})
		}
	}

	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{OfInputItemContentList: parts},
		},
	}
}

func convertRole(role responses.EasyInputMessageRole) MessageRole {
	switch role {
	case responses.EasyInputMessageRoleUser:
		return RoleUser

	case responses.EasyInputMessageRoleAssistant:
		return RoleAssistant

	default:
		return RoleSystem
	}
}

func (a *Agent) Messages() []Message {
	var result []Message
	var lastToolName string
	var lastToolArgs string

	for _, item := range a.messages {
		if msg := item.OfMessage; msg != nil {
			// Handle string content
			if msg.Content.OfString.Value != "" {
				content := msg.Content.OfString.Value

				// Skip conversation summaries
				if strings.HasPrefix(content, "<conversation_summary>") {
					continue
				}

				result = append(result, Message{
					Role:    convertRole(msg.Role),
					Content: []Content{{Text: content}},
				})
				continue
			}

			// Handle multi-part content (text + images)
			if len(msg.Content.OfInputItemContentList) > 0 {
				var contents []Content

				for _, part := range msg.Content.OfInputItemContentList {
					if part.OfInputText != nil {
						contents = append(contents, Content{Text: part.OfInputText.Text})
					}

					if part.OfInputImage != nil && part.OfInputImage.ImageURL.Value != "" {
						contents = append(contents, Content{File: &File{Data: part.OfInputImage.ImageURL.Value}})
					}
				}

				if len(contents) > 0 {
					result = append(result, Message{
						Role:    convertRole(msg.Role),
						Content: contents,
					})
				}
				continue
			}
		}

		if fc := item.OfFunctionCall; fc != nil {
			lastToolName = fc.Name
			lastToolArgs = fc.Arguments
		}

		if fco := item.OfFunctionCallOutput; fco != nil {
			output := fco.Output.OfString.Value

			result = append(result, Message{
				Role: RoleAssistant,
				Content: []Content{{ToolResult: &ToolResult{
					Name:    lastToolName,
					Args:    lastToolArgs,
					Content: output,
				}}},
			})
		}
	}

	return result
}

func (a *Agent) Usage() Usage {
	return a.usage
}

func (a *Agent) Clear() {
	a.messages = nil
}
