package subagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const instructions = "You are an agent performing a specific delegated task. Complete only the assigned scope. Unless the task explicitly asks you to edit files, stay read-only. When done, provide a concise result with file:line references when relevant, followed by any uncertainty or verification gaps. Do not explain your process."

func Tools(cfg *agent.Config) []tool.Tool {
	description := strings.Join([]string{
		"Launch an agent to handle a task in a separate context. The agent has access to all tools and runs its own agentic loop. Only the final answer is returned, keeping your context clean.",
		"",
		"When to use:",
		"- Research tasks requiring many tool calls (exploring codebases, finding all usages of a function).",
		"- Independent subtasks whose intermediate results would clutter your context.",
		"- You can launch multiple agents in parallel by making multiple tool calls in one response.",
		"- Make the prompt explicit about whether the agent may edit files or should stay read-only.",
		"",
		"When NOT to use:",
		"- Simple tasks needing 1-2 tool calls -- just use the tools directly.",
		"- When the user needs to see intermediate results -- they won't be visible.",
		"",
		"Prompting tips:",
		"- The agent has NO access to your conversation history. Write the prompt as a self-contained briefing.",
		"- Be specific: include file paths, function names, exact requirements, constraints, and desired output shape.",
		"- Do not ask the agent to synthesize from another agent's findings. Do the synthesis yourself, then delegate a precise next task if needed.",
		"- Bad: \"Find the bug.\" Good: \"In /src/api/handler.go, the CreateUser function returns 500 on duplicate emails. Find where the error is swallowed and suggest a fix.\"",
	}, "\n")

	return []tool.Tool{{
		Name:        "agent",
		Description: description,
		Effect:      tool.StaticEffect(tool.EffectMutates),

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

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			prompt, ok := args["prompt"].(string)

			if !ok || prompt == "" {
				return "", fmt.Errorf("prompt is required")
			}

			subcfg := cfg.Derive()
			subcfg.Instructions = func() string { return instructions }
			subcfg.Tools = func() []tool.Tool {
				if cfg.Tools == nil {
					return nil
				}

				var filtered []tool.Tool

				for _, t := range cfg.Tools() {
					if t.Name == "agent" || t.Hidden {
						continue
					}

					filtered = append(filtered, t)
				}

				return filtered
			}

			sub := &agent.Agent{Config: subcfg}

			var result strings.Builder

			for msg, err := range sub.Send(ctx, []agent.Content{{Text: prompt}}) {
				if err != nil {
					return "", fmt.Errorf("agent error: %w", err)
				}

				for _, c := range msg.Content {
					if c.Text != "" {
						result.WriteString(c.Text)
					}
				}
			}

			text := strings.TrimSpace(result.String())

			if text == "" {
				return "Sub-agent completed but produced no output.", nil
			}

			return text, nil
		},
	}}
}
