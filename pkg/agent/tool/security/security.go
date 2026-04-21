package security

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

func SecurityTool(client openai.Client, model string, tools []tool.Tool) tool.Tool {
	description := strings.Join([]string{
		"Scan the current project for security vulnerabilities using an AI-powered security review.",
		"",
		"Usage:",
		"- Launches a dedicated security agent that explores the codebase and identifies real vulnerabilities.",
		"- Focuses on high-confidence, exploitable issues (SQL injection, RCE, auth bypass, etc.).",
		"- Filters out false positives, theoretical issues, and low-impact findings.",
		"- Optionally scope the scan to a specific directory or file pattern.",
	}, "\n")

	return tool.Tool{
		Name:        "security_scan",
		Description: description,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file path to scope the scan (defaults to entire project)",
				},
				"focus": map[string]any{
					"type":        "string",
					"description": "Optional focus area for the scan (e.g., \"authentication\", \"input validation\", \"API endpoints\")",
				},
			},
		},

		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			path := ""
			if p, ok := args["path"].(string); ok {
				path = p
			}

			focus := ""
			if f, ok := args["focus"].(string); ok {
				focus = f
			}

			prompt := buildPrompt(path, focus)

			subEnv := *env
			subEnv.Tracker = tracker.New(env.Root)

			return runSecurityAgent(ctx, client, model, &subEnv, tools, prompt)
		},
	}
}

func Tools(client openai.Client, model string, tools []tool.Tool) []tool.Tool {
	return []tool.Tool{
		SecurityTool(client, model, tools),
	}
}

func buildPrompt(path, focus string) string {
	var sb strings.Builder

	sb.WriteString(auditPrompt)

	if path != "" {
		fmt.Fprintf(&sb, "\n\nSCOPE: Focus your analysis on the path: %s", path)
	}

	if focus != "" {
		fmt.Fprintf(&sb, "\n\nFOCUS AREA: Pay special attention to: %s", focus)
	}

	return sb.String()
}

const agentInstructions = "You are a senior security engineer performing a thorough security audit. Use the available tools to explore and analyze the codebase. Be systematic and thorough. Report only high-confidence findings with concrete exploit scenarios. When done, provide your complete findings report."

const maxIterations = 75

func runSecurityAgent(ctx context.Context, client openai.Client, model string, env *env.Environment, tools []tool.Tool, prompt string) (string, error) {
	// Only allow read-only file tools for security scanning
	allowedTools := map[string]bool{
		"read": true, "ls": true, "find": true, "grep": true,
	}

	var formattedTools []responses.ToolUnionParam

	for _, t := range tools {
		if t.Hidden || !allowedTools[t.Name] {
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

	messages := []responses.ResponseInputItemUnionParam{{
		OfMessage: &responses.EasyInputMessageParam{
			Role: responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{
				OfString: openai.String(prompt),
			},
		},
	}}

	for range maxIterations {
		stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
			Model:        model,
			Instructions: openai.String(agentInstructions),

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
						return "", fmt.Errorf("security agent: failed to parse output message: %w", err)
					}

					outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
						OfOutputMessage: &p,
					})

				case responses.ResponseReasoningItem:
					var p responses.ResponseReasoningItemParam
					if err := json.Unmarshal([]byte(o.RawJSON()), &p); err != nil {
						return "", fmt.Errorf("security agent: failed to parse reasoning item: %w", err)
					}

					outputItems = append(outputItems, responses.ResponseInputItemUnionParam{
						OfReasoning: &p,
					})

				case responses.ResponseFunctionToolCall:
					var p responses.ResponseFunctionToolCallParam
					if err := json.Unmarshal([]byte(o.RawJSON()), &p); err != nil {
						return "", fmt.Errorf("security agent: failed to parse function call: %w", err)
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
			return "", fmt.Errorf("security agent error: %w", err)
		}

		if compacted {
			messages = outputItems
		} else {
			messages = append(messages, outputItems...)
		}

		if len(toolCalls) == 0 {
			result := strings.TrimSpace(outputText.String())

			if result == "" {
				return "Security scan completed but produced no output.", nil
			}

			return result, nil
		}

		for _, tc := range toolCalls {
			if env.StatusUpdate != nil {
				env.StatusUpdate(fmt.Sprintf("security: %s %s", tc.name, extractHint(tc.args)))
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

	return "", fmt.Errorf("security agent exceeded maximum iterations (%d)", maxIterations)
}

func extractHint(argsJSON string) string {
	var args map[string]any

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}

	for _, key := range []string{"pattern", "path", "command"} {
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
