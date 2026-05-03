package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

var errYieldStopped = errors.New("yield stopped")

type Agent struct {
	*Config

	Messages []Message
	Usage    Usage
}

// Models lists the available models from the API.
func (a *Agent) Models(ctx context.Context) ([]ModelInfo, error) {
	resp, err := a.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	var models []ModelInfo

	for _, m := range resp.Data {
		models = append(models, ModelInfo{ID: m.ID})
	}

	return models, nil
}

func (a *Agent) Send(ctx context.Context, input []Content) iter.Seq2[Message, error] {
	a.appendContextMessages()
	a.Messages = append(a.Messages, userMessage(input))

	return func(yield func(Message, error) bool) {
		for {
			a.removeOrphanedToolMessages()

			model := ""
			if a.Config.Model != nil {
				model = a.Model()
			}

			effort := ""
			if a.Config.Effort != nil {
				effort = a.Effort()
			}

			instructions := ""
			if a.Instructions != nil {
				instructions = a.Instructions()
			}

			var tools []tool.Tool
			if a.Tools != nil {
				tools = a.Tools()
			}

			req := &request{
				model:        model,
				effort:       effort,
				instructions: instructions,
				messages:     a.Messages,
				tools:        tools,
			}

			resp, err := complete(ctx, a.client, req, yield)

			if err != nil {
				if !errors.Is(err, errYieldStopped) && !errors.Is(err, context.Canceled) && isRecoverableError(err) {
					a.compactMessages(ctx)

					req.messages = a.Messages
					resp, err = complete(ctx, a.client, req, yield)
				}

				if err != nil {
					if err != errYieldStopped {
						yield(Message{}, err)
					}
					return
				}
			}

			a.Usage.InputTokens += resp.usage.InputTokens
			a.Usage.CachedTokens += resp.usage.CachedTokens
			a.Usage.OutputTokens += resp.usage.OutputTokens
			a.Messages = append(a.Messages, resp.messages...)

			calls := extractToolCalls(resp.messages)

			if len(calls) == 0 {
				return
			}

			if err := a.processToolCalls(ctx, calls, tools, yield); err != nil {
				if err != errYieldStopped {
					yield(Message{}, err)
				}
				return
			}
		}
	}
}

func (a *Agent) appendContextMessages() {
	if a.ContextMessages == nil {
		return
	}

	a.Messages = append(a.Messages, a.ContextMessages()...)
}

func extractToolCalls(messages []Message) []ToolCall {
	var calls []ToolCall

	for _, m := range messages {
		for _, c := range m.Content {
			if c.ToolCall != nil {
				calls = append(calls, *c.ToolCall)
			}
		}
	}

	return calls
}

func (a *Agent) processToolCalls(ctx context.Context, calls []ToolCall, tools []tool.Tool, yield func(Message, error) bool) error {
	for _, tc := range calls {
		callMsg := Message{
			Role:    RoleAssistant,
			Content: []Content{{ToolCall: &ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args}}},
		}

		if !yield(callMsg, nil) {
			return errYieldStopped
		}

		hc := tool.ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args}

		var result string

		for _, h := range a.Hooks.PreToolUse {
			r, err := h(ctx, hc)

			if err != nil {
				result = fmt.Sprintf("error: %v", err)
				break
			}

			if r != "" {
				result = r
				break
			}
		}

		if result == "" {
			result = a.executeTool(ctx, tc, tools)
		}

		for _, h := range a.Hooks.PostToolUse {
			r, err := h(ctx, hc, result)

			if err != nil {
				result = fmt.Sprintf("error: %v", err)
				break
			}

			result = r
		}

		resultMsg := Message{
			Role: RoleAssistant,
			Content: []Content{{ToolResult: &ToolResult{
				ID:      tc.ID,
				Name:    tc.Name,
				Args:    tc.Args,
				Content: result,
			}}},
		}

		a.Messages = append(a.Messages, resultMsg)

		if !yield(resultMsg, nil) {
			return errYieldStopped
		}
	}

	return nil
}

func (a *Agent) executeTool(ctx context.Context, tc ToolCall, tools []tool.Tool) string {
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

	result, err := t.Execute(ctx, args)

	if err != nil {
		return fmt.Sprintf("error: %v", err)
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

func userMessage(input []Content) Message {
	return Message{
		Role:    RoleUser,
		Content: input,
	}
}
