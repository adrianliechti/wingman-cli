package mcp

import (
	"context"

	"github.com/adrianliechti/wingman-cli/pkg/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	var result []tool.Tool

	for name := range c.transports {
		session, err := c.createSession(ctx, name)

		if err != nil {
			return nil, err
		}

		defer session.Close()

		resp, err := session.ListTools(ctx, nil)

		if err != nil {
			return nil, err
		}

		for _, t := range resp.Tools {
			tool := tool.Tool{
				Name:        t.Name,
				Description: t.Description,

				Schema: t.InputSchema,

				ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
					session, err := c.createSession(ctx, name)

					if err != nil {
						return nil, err
					}

					defer session.Close()

					return session.CallTool(ctx, &mcp.CallToolParams{
						Name:      t.Name,
						Arguments: params,
					})
				},
			}

			result = append(result, tool)
		}
	}

	return result, nil
}
