package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/adrianliechti/wingman-cli/pkg/tool"

	"mcp"
)

func (m *Manager) Tools(ctx context.Context) ([]tool.Tool, error) {
	var result []tool.Tool

	for _, c := range m.clients {
		s, err := c.connect(ctx)

		if err != nil {
			return nil, err
		}

		defer s.Close()

		resp, err := s.ListTools(ctx, &mcp.ListToolsParams{})

		if err != nil {
			return nil, err
		}

		for _, t := range resp.Tools {
			var schema tool.Schema

			input, _ := json.Marshal(t.InputSchema)

			if err := json.Unmarshal([]byte(input), &schema); err != nil {
				return nil, err
			}

			if len(t.InputSchema.Properties) == 0 {
				schema = map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": false,
				}
			}

			tool := tool.Tool{
				Name:        t.Name,
				Description: t.Description,

				Schema: schema,

				Execute: func(ctx context.Context, args map[string]any) (any, error) {
					if args == nil {
						args = map[string]any{}
					}

					s, err := c.connect(ctx)

					if err != nil {
						return nil, err
					}

					defer s.Close()

					result, err := s.CallTool(ctx, t.Name, args, &mcp.CallToolOptions{})

					if err != nil {
						return nil, err
					}

					if len(result.Content) > 1 {
						return nil, errors.New("multiple content types not supported")
					}

					for _, content := range result.Content {
						switch content.Type {
						case "text":
							return content.Text, nil
						default:
							return nil, errors.New("unsupported content type")
						}
					}

					return nil, errors.New("no content returned")
				},
			}

			result = append(result, tool)
		}
	}

	return result, nil
}
