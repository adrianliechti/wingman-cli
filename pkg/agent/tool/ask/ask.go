package ask

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func AskTool() tool.Tool {
	description := strings.Join([]string{
		"Ask the user a question and wait for their response. Use this when you need clarification or input to proceed.",
		"",
		"Usage:",
		"- Use when you need the user to make a decision between approaches.",
		"- Use when requirements are ambiguous and different answers would change your approach.",
		"- Prefer making reasonable assumptions over asking. Only ask when the answer materially affects your work.",
		"- Do NOT use this for yes/no confirmations on tool execution — those are handled automatically.",
	}, "\n")

	return tool.Tool{
		Name:        "ask_user",
		Description: description,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question to ask the user. Be specific and concise.",
				},
			},

			"required": []string{"question"},
		},

		Execute: func(ctx context.Context, env *env.Environment, args map[string]any) (string, error) {
			question, ok := args["question"].(string)

			if !ok || question == "" {
				return "", fmt.Errorf("question is required")
			}

			if env.AskUser == nil {
				return "", fmt.Errorf("user interaction is not available")
			}

			answer, err := env.AskUser(question)

			if err != nil {
				return "", fmt.Errorf("failed to get user response: %w", err)
			}

			return answer, nil
		},

		Hidden: true,
	}
}

func Tools() []tool.Tool {
	return []tool.Tool{
		AskTool(),
	}
}
