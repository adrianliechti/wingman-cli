package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/adrianliechti/wingman-cli/pkg/tool"

	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func toTools(tools []tool.Tool) []wingman.Tool {
	var result []wingman.Tool

	for _, t := range tools {
		result = append(result, toTool(t))
	}

	return result
}

func toTool(t tool.Tool) wingman.Tool {
	return wingman.Tool{
		Name:        t.Name,
		Description: t.Description,

		Parameters: t.Schema,
	}
}

func toolsWrapper(client *wingman.Client, model string, tools []tool.Tool) []tool.Tool {
	var wrapped []tool.Tool

	for _, t := range tools {
		wrapped = append(wrapped, toolWrapper(client, model, t))
	}

	return wrapped
}

func toolWrapper(client *wingman.Client, model string, t tool.Tool) tool.Tool {
	schema := tool.Schema{
		"type": "object",

		"properties": map[string]any{
			"goal": map[string]any{
				"type":        "string",
				"description": "The goal of the task including the expected record, fields and information you expect or search in the result. This goal is used to compress and filter large results.",
			},

			"input": t.Schema,
		},
	}

	return tool.Tool{
		Name:        t.Name,
		Description: t.Description,

		Schema: schema,

		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			goal, ok := args["goal"].(string)

			if !ok {
				return nil, errors.New("goal is required")
			}

			println("#######")
			println("🥅", goal)
			println()

			input, ok := args["input"].(map[string]any)

			if !ok {
				return nil, errors.New("input is required")
			}

			result, err := t.Execute(ctx, input)

			if err != nil {
				return nil, err
			}

			var data string

			switch val := result.(type) {
			case string:
				data = val
			case []any, map[string]any:
				json, _ := json.Marshal(val)
				data = string(json)
			}

			println("#######")
			println(data)
			println()

			completion, err := client.Completions.New(ctx, wingman.CompletionRequest{
				Model: model,

				Messages: []wingman.Message{
					wingman.SystemMessage("Extract relevant information based on this goal:\n" + goal),
					wingman.UserMessage(data),
				},
			})

			if err != nil {
				return nil, err
			}

			summary := completion.Message.Text()

			println("#######")
			println("summary", summary)
			println()

			return summary, nil
		},
	}
}
