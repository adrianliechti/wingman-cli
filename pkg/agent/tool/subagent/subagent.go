package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func SubAgentTool(client openai.Client, model string, availableTools []tool.Tool) tool.Tool {
	description := strings.Join([]string{
		"Launch a sub-agent to handle a task in a separate context. The sub-agent can use all available tools (read, write, edit, grep, find, shell, etc.) and runs its own agentic loop. Only the final answer is returned to you, keeping your context window clean.",
		"",
		"Usage:",
		"- Use for research tasks that require many tool calls (e.g., exploring a codebase, finding all usages of a function).",
		"- Use for independent subtasks that would otherwise fill your context with intermediate tool results.",
		"- Provide a clear, self-contained prompt — the sub-agent has no access to your conversation history.",
		"- Do NOT use for simple tasks that need only 1-2 tool calls — use the tools directly instead.",
		"- Do NOT use when you need to show intermediate results to the user — those won't be visible.",
	}, "\n")

	return tool.Tool{
		Name:        "sub_agent",
		Description: description,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "A clear, self-contained task description for the sub-agent. Include all necessary context since it has no access to the current conversation.",
				},
			},

			"required": []string{"prompt"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			prompt, ok := args["prompt"].(string)

			if !ok || prompt == "" {
				return "", fmt.Errorf("prompt is required")
			}

			return runSubAgent(ctx, client, model, env, availableTools, prompt)
		},
	}
}

const subAgentInstructions = "You are a sub-agent performing a specific task. Complete the task thoroughly using the tools available to you. When done, provide a clear, concise summary of your findings or results. Do not explain your process — just provide the answer."

const maxIterations = 50

func runSubAgent(ctx context.Context, client openai.Client, model string, env *tool.Environment, tools []tool.Tool, prompt string) (string, error) {
	// Format tools for the API, excluding hidden tools and the sub_agent tool itself
	var formattedTools []responses.ToolUnionParam

	for _, t := range tools {
		if t.Hidden || t.Name == "sub_agent" {
			continue
		}

		f := &responses.FunctionToolParam{
			Name:       t.Name,
			Parameters: t.Parameters,
			Strict:     openai.Bool(false),
		}

		if t.Description != "" {
			f.Description = openai.String(t.Description)
		}

		formattedTools = append(formattedTools, responses.ToolUnionParam{
			OfFunction: f,
		})
	}

	// Build initial message
	messages := []responses.ResponseInputItemUnionParam{{
		OfMessage: &responses.EasyInputMessageParam{
			Role: responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{
				OfString: openai.String(prompt),
			},
		},
	}}

	// Agentic loop
	for range maxIterations {
		resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
			Model:        model,
			Instructions: openai.String(subAgentInstructions),

			Input: responses.ResponseNewParamsInputUnion{OfInputItemList: messages},

			Tools:             formattedTools,
			ParallelToolCalls: openai.Bool(true),

			Store:      openai.Bool(false),
			Truncation: responses.ResponseNewParamsTruncationAuto,

			ContextManagement: []responses.ResponseNewParamsContextManagement{{
				Type:             "compaction",
				CompactThreshold: openai.Int(200000),
			}},
		})

		if err != nil {
			return "", fmt.Errorf("sub-agent error: %w", err)
		}

		// Convert output items to input items for the next turn
		var outputItems []responses.ResponseInputItemUnionParam
		var toolCalls []toolCall
		var compacted bool

		for _, item := range resp.Output {
			switch o := item.AsAny().(type) {
			case responses.ResponseOutputMessage:
				var p responses.ResponseOutputMessageParam
				json.Unmarshal([]byte(o.RawJSON()), &p)

				outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
					OfOutputMessage: &p,
				})

			case responses.ResponseReasoningItem:
				var p responses.ResponseReasoningItemParam
				json.Unmarshal([]byte(o.RawJSON()), &p)

				outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
					OfReasoning: &p,
				})

			case responses.ResponseFunctionToolCall:
				var p responses.ResponseFunctionToolCallParam
				json.Unmarshal([]byte(o.RawJSON()), &p)

				outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
					OfFunctionCall: &p,
				})

				toolCalls = append(toolCalls, toolCall{
					id:   o.CallID,
					name: o.Name,
					args: o.Arguments,
				})

			case responses.ResponseCompactionItem:
				outputItems = append(outputItems, responses.ResponseInputItemParamOfCompaction(o.EncryptedContent))
				compacted = true
			}
		}

		if compacted {
			messages = outputItems
		} else {
			messages = append(messages, outputItems...)
		}

		// No tool calls means the agent is done
		if len(toolCalls) == 0 {
			result := strings.TrimSpace(resp.OutputText())

			if result == "" {
				return "Sub-agent completed but produced no output.", nil
			}

			return result, nil
		}

		// Execute tool calls and append results
		for _, tc := range toolCalls {
			result := executeTool(ctx, env, tc, tools)

			messages = append(messages, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: tc.id,
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: openai.String(result),
					},
				},
			})
		}
	}

	return "", fmt.Errorf("sub-agent exceeded maximum iterations (%d)", maxIterations)
}

type toolCall struct {
	id   string
	name string
	args string
}

func executeTool(ctx context.Context, env *tool.Environment, tc toolCall, tools []tool.Tool) string {
	var t *tool.Tool

	for i := range tools {
		if tools[i].Name == tc.name {
			t = &tools[i]
			break
		}
	}

	if t == nil {
		return fmt.Sprintf("error: unknown tool %s", tc.name)
	}

	var args map[string]any

	if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
		return fmt.Sprintf("error: failed to parse arguments: %v", err)
	}

	result, err := t.Execute(ctx, env, args)

	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	return result
}
