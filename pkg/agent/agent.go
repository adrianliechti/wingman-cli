package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const maxResultBytes = 50 * 1024 // 50KB per tool result

var errYieldStopped = errors.New("yield stopped")

type Agent struct {
	*Config

	messages []responses.ResponseInputItemUnionParam
	usage    Usage
}

func New(cfg *Config) *Agent {
	return &Agent{
		Config: cfg,
	}
}

func (a *Agent) Send(ctx context.Context, instructions string, input []Content, tools []tool.Tool) iter.Seq2[Message, error] {
	a.messages = append(a.messages, a.userMessage(input))

	return func(yield func(Message, error) bool) {
		if err := a.run(ctx, yield, instructions, tools); err != nil && err != errYieldStopped {
			yield(Message{}, err)
		}
	}
}

func (a *Agent) run(ctx context.Context, yield func(Message, error) bool, instructions string, tools []tool.Tool) error {
	formattedTools := formatTools(tools)

	for {
		outputItems, err := a.streamResponse(ctx, yield, instructions, formattedTools)
		if err != nil {
			return err
		}

		toolCalls := extractToolCalls(outputItems)
		if len(toolCalls) == 0 {
			return nil
		}

		if err := a.processToolCalls(ctx, yield, toolCalls, tools); err != nil {
			return err
		}
	}
}

func (a *Agent) streamResponse(ctx context.Context, yield func(Message, error) bool, instructions string, tools []responses.ToolUnionParam) ([]responses.ResponseInputItemUnionParam, error) {
	stream := a.Client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model:        a.Model,
		Instructions: openai.String(instructions),

		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: a.messages},

		Tools:             tools,
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
	})

	var outputItems []responses.ResponseInputItemUnionParam
	var compacted bool

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
				outputItems = append(outputItems, responses.ResponseInputItemParamOfCompaction(item.EncryptedContent))
				compacted = true
			}

		case responses.ResponseCompletedEvent:
			if e.Response.Usage.InputTokens > 0 {
				a.usage.InputTokens += e.Response.Usage.InputTokens
				a.usage.OutputTokens += e.Response.Usage.OutputTokens
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	if compacted {
		a.messages = outputItems
	} else {
		a.messages = append(a.messages, outputItems...)
	}

	return outputItems, nil
}

func extractToolCalls(items []responses.ResponseInputItemUnionParam) []ToolCall {
	var toolCalls []ToolCall

	for _, item := range items {
		if fc := item.OfFunctionCall; fc != nil {
			toolCalls = append(toolCalls, ToolCall{
				ID:   fc.CallID,
				Name: fc.Name,
				Args: fc.Arguments,
			})
		}
	}

	return toolCalls
}

func (a *Agent) processToolCalls(ctx context.Context, yield func(Message, error) bool, toolCalls []ToolCall, tools []tool.Tool) error {
	for _, tc := range toolCalls {
		// Yield tool call message
		callMsg := Message{
			Role:    RoleAssistant,
			Content: []Content{{ToolCall: &ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args}}},
		}

		if !yield(callMsg, nil) {
			return errYieldStopped
		}

		// Execute and yield result immediately
		result := truncateResult(ExecuteTool(ctx, a.Environment, tc, tools), a.Environment)

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

func findTool(name string, tools []tool.Tool) *tool.Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}

	return nil
}

// ExecuteTool looks up and executes a tool by name. Shared between agent and sub-agent.
func ExecuteTool(ctx context.Context, env *env.Environment, tc ToolCall, tools []tool.Tool) string {
	t := findTool(tc.Name, tools)

	if t == nil {
		return fmt.Sprintf("error: unknown tool %s", tc.Name)
	}

	args := make(map[string]any)

	if tc.Args != "" {
		if err := json.Unmarshal([]byte(tc.Args), &args); err != nil {
			return fmt.Sprintf("error: failed to parse arguments: %v", err)
		}
	}

	result, err := t.Execute(ctx, env, args)

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
	toolCallsByID := make(map[string]ToolCall)

	for _, item := range a.messages {
		if msg := item.OfMessage; msg != nil {
			// Handle string content
			if msg.Content.OfString.Value != "" {
				content := msg.Content.OfString.Value

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

		// Handle assistant output messages from the API response
		if outMsg := item.OfOutputMessage; outMsg != nil {
			var contents []Content

			for _, part := range outMsg.Content {
				if text := part.OfOutputText; text != nil {
					contents = append(contents, Content{Text: text.Text})
				}
			}

			if len(contents) > 0 {
				result = append(result, Message{
					Role:    RoleAssistant,
					Content: contents,
				})
			}
			continue
		}

		if fc := item.OfFunctionCall; fc != nil {
			toolCallsByID[fc.CallID] = ToolCall{Name: fc.Name, Args: fc.Arguments}
		}

		if fco := item.OfFunctionCallOutput; fco != nil {
			tc := toolCallsByID[fco.CallID]

			result = append(result, Message{
				Role: RoleAssistant,
				Content: []Content{{ToolResult: &ToolResult{
					Name:    tc.Name,
					Args:    tc.Args,
					Content: fco.Output.OfString.Value,
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
	if a.Environment != nil && a.Environment.Tracker != nil {
		a.Environment.Tracker.Clear()
	}
}

func truncateResult(result string, env *env.Environment) string {
	if len(result) <= maxResultBytes {
		return result
	}

	totalBytes := len(result)

	// Keep the tail (most relevant for command output)
	truncated := result[totalBytes-maxResultBytes:]

	// Align to next newline to avoid partial lines
	if idx := strings.Index(truncated, "\n"); idx >= 0 && idx < 512 {
		truncated = truncated[idx+1:]
	}

	shownBytes := len(truncated)

	// Write full output to scratch dir if available
	var notice string

	if env != nil && env.Scratch != nil {
		name := fmt.Sprintf("result-%d.txt", time.Now().UnixNano())
		path := filepath.Join(env.ScratchDir(), name)

		if err := os.WriteFile(path, []byte(result), 0644); err == nil {
			notice = fmt.Sprintf("[Output truncated: showing last %d of %d bytes. Full output: %s]\n\n", shownBytes, totalBytes, path)
		}
	}

	if notice == "" {
		notice = fmt.Sprintf("[Output truncated: showing last %d of %d bytes]\n\n", shownBytes, totalBytes)
	}

	return notice + truncated
}
