package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
	"github.com/adrianliechti/wingman-agent/pkg/agent/env/tracker"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func SubAgentTool(client openai.Client, model string, availableTools []tool.Tool) tool.Tool {
	description := strings.Join([]string{
		"Launch a agent to handle a task in a separate context. The agent has access to all tools and runs its own agentic loop. Only the final answer is returned, keeping your context clean.",
		"",
		"When to use:",
		"- Research tasks requiring many tool calls (exploring codebases, finding all usages of a function).",
		"- Independent subtasks whose intermediate results would clutter your context.",
		"- You can launch multiple agents in parallel by making multiple tool calls in one response.",
		"",
		"When NOT to use:",
		"- Simple tasks needing 1-2 tool calls — just use the tools directly.",
		"- When the user needs to see intermediate results — they won't be visible.",
		"",
		"Prompting tips:",
		"- The agent has NO access to your conversation history. Write the prompt as a self-contained briefing.",
		"- Be specific: include file paths, function names, and exact requirements.",
		"- Bad: \"Find the bug.\" Good: \"In /src/api/handler.go, the CreateUser function returns 500 on duplicate emails. Find where the error is swallowed and suggest a fix.\"",
	}, "\n")

	return tool.Tool{
		Name:        "agent",
		Description: description,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "A clear, self-contained task description for the agent. Include all necessary context since it has no access to the current conversation.",
				},
			},

			"required": []string{"prompt"},
		},

		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			prompt, ok := args["prompt"].(string)

			if !ok || prompt == "" {
				return "", fmt.Errorf("prompt is required")
			}

			subEnv := *env
			subEnv.Tracker = tracker.New(env.Root)

			return runSubAgent(ctx, client, model, &subEnv, availableTools, prompt)
		},
	}
}

const subAgentInstructions = "You are an agent performing a specific task. Complete the task thoroughly using the tools available to you. When done, provide a clear, concise summary of your findings or results. Do not explain your process — just provide the answer."

const maxIterations = 50

func runSubAgent(ctx context.Context, client openai.Client, model string, env *env.Environment, tools []tool.Tool, prompt string) (string, error) {
	// Format tools for the API, excluding hidden tools and the sub_agent tool itself
	var formattedTools []responses.ToolUnionParam

	for _, t := range tools {
		if t.Hidden || t.Name == "agent" {
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

	// Agentic loop with streaming
	for range maxIterations {
		stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
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

		var outputItems []responses.ResponseInputItemUnionParam
		var toolCalls []toolCall
		var compacted bool
		var outputText strings.Builder

		for stream.Next() {
			event := stream.Current()

			switch e := event.AsAny().(type) {
			case responses.ResponseTextDeltaEvent:
				outputText.WriteString(e.Delta)

			case responses.ResponseOutputItemDoneEvent:
				switch o := e.Item.AsAny().(type) {
				case responses.ResponseOutputMessage:
					var p responses.ResponseOutputMessageParam
					if err := json.Unmarshal([]byte(o.RawJSON()), &p); err != nil {
						return "", fmt.Errorf("agent: failed to parse output message: %w", err)
					}

					outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
						OfOutputMessage: &p,
					})

				case responses.ResponseReasoningItem:
					var p responses.ResponseReasoningItemParam
					if err := json.Unmarshal([]byte(o.RawJSON()), &p); err != nil {
						return "", fmt.Errorf("agent: failed to parse reasoning item: %w", err)
					}

					outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
						OfReasoning: &p,
					})

				case responses.ResponseFunctionToolCall:
					var p responses.ResponseFunctionToolCallParam
					if err := json.Unmarshal([]byte(o.RawJSON()), &p); err != nil {
						return "", fmt.Errorf("agent: failed to parse function call: %w", err)
					}

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
		}

		if err := stream.Err(); err != nil {
			return "", fmt.Errorf("agent error: %w", err)
		}

		if compacted {
			messages = outputItems
		} else {
			messages = append(messages, outputItems...)
		}

		// No tool calls means the agent is done
		if len(toolCalls) == 0 {
			result := strings.TrimSpace(outputText.String())

			if result == "" {
				return "Sub-agent completed but produced no output.", nil
			}

			return result, nil
		}

		// Execute tool calls and append results
		for _, tc := range toolCalls {
			if env.StatusUpdate != nil {
				hint := extractToolHint(tc.args)
				env.StatusUpdate(fmt.Sprintf("%s %s", tc.name, hint))
			}

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

	return "", fmt.Errorf("agent exceeded maximum iterations (%d)", maxIterations)
}

// extractToolHint returns a short display hint from tool call arguments.
func extractToolHint(argsJSON string) string {
	var args map[string]any

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}

	keys := []string{"pattern", "command", "path", "query", "url", "prompt"}
	for _, key := range keys {
		if val, ok := args[key].(string); ok && val != "" {
			return strings.Join(strings.Fields(val), " ")
		}
	}

	return ""
}

type toolCall struct {
	id   string
	name string
	args string
}

func executeTool(ctx context.Context, env *env.Environment, tc toolCall, tools []tool.Tool) string {
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
