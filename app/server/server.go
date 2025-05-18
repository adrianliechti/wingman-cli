package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/adrianliechti/wingman-cli/app"

	"mcp"
	"mcp/jsonschema"

	"github.com/rs/cors"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func Run(ctx context.Context, client *wingman.Client) error {
	tools := app.MustConnectTools(ctx)

	//tools = util.OptimizeTools(client, app.DefaultModel, tools)

	cli.Info()
	cli.Info("🖥️ MCP Server")
	cli.Info()

	for _, tool := range tools {
		println("🛠️ " + tool.Name)
	}

	cli.Info()

	s := mcp.NewServer("sse", "1.0.0", &mcp.ServerOptions{})

	for _, t := range tools {
		data, _ := json.Marshal(t.Schema)

		var schema jsonschema.Schema
		json.Unmarshal(data, &schema)

		tool := &mcp.ServerTool{
			Tool: &mcp.Tool{
				Name:        t.Name,
				Description: t.Description,

				InputSchema: &schema,
			},

			Handler: func(ctx context.Context, s *mcp.ServerSession, r *mcp.CallToolParams) (*mcp.CallToolResult, error) {
				var args map[string]any
				json.Unmarshal(r.Arguments, &args)

				result, err := t.Execute(ctx, args)

				if err != nil {
					return nil, err
				}

				var content string

				switch v := result.(type) {
				case string:
					content = v
				default:
					data, _ := json.Marshal(v)
					content = string(data)
				}

				return &mcp.CallToolResult{
					Content: []*mcp.Content{
						mcp.NewTextContent(content),
					},
				}, nil
			},
		}

		s.AddTools(tool)
	}

	addr := "localhost:4200"

	handler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server {
		url := r.URL.Path
		log.Printf("Handling request for URL %s\n", url)

		switch url {
		case "/sse":
			return s

		default:
			return nil
		}
	})

	if err := http.ListenAndServe(addr, cors.AllowAll().Handler(handler)); err != nil {
		return err
	}

	return nil
}
