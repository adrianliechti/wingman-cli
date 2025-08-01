package util

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
	wingman "github.com/adrianliechti/wingman/pkg/client"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

func ConvertTools(tools []tool.Tool) []wingman.Tool {
	var result []wingman.Tool

	for _, t := range tools {
		result = append(result, ConvertTool(t))
	}

	return result
}

func ConvertTool(t tool.Tool) wingman.Tool {
	var parameters map[string]any

	if data, err := t.Schema.MarshalJSON(); err == nil {
		json.Unmarshal(data, &parameters)
	}

	return wingman.Tool{
		Name:        t.Name,
		Description: t.Description,

		Parameters: parameters,
	}
}

func OptimizeTools(client *wingman.Client, model string, tools []tool.Tool) []tool.Tool {
	var wrapped []tool.Tool

	for _, t := range tools {
		wrapped = append(wrapped, OptimizeTool(client, model, t))
	}

	return wrapped
}

func OptimizeTool(client *wingman.Client, model string, t tool.Tool) tool.Tool {
	return tool.Tool{
		Name:        t.Name,
		Description: t.Description,

		Schema: &jsonschema.Schema{
			Type: "object",

			Properties: map[string]*jsonschema.Schema{
				"goal": {
					Type:        "string",
					Description: "The goal of the task including the expected record, fields and information you expect or search in the result. This goal is used to compress and filter large results.",
				},

				"input": t.Schema,
			},
		},

		ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
			goal, ok := params["goal"].(string)

			if !ok {
				return nil, errors.New("goal is required")
			}

			input, ok := params["input"].(map[string]any)

			if !ok {
				input = map[string]any{}
			}

			result, err := t.ToolHandler(ctx, input)

			if err != nil {
				return nil, err
			}

			var data string

			switch val := result.(type) {
			case string:
				data = val
			case any, []any, map[string]any:
				json, _ := json.Marshal(val)
				data = string(json)
			}

			data = strings.TrimSpace(data)

			if len(data) <= 4000 {
				return data, nil
			}

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

			content := completion.Message.Text()
			return content, nil
		},
	}
}
