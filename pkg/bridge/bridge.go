package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/cors"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func Run(ctx context.Context, client *wingman.Client, instructions string, tools []tool.Tool) error {
	impl := &mcp.Implementation{
		Name: "wingman",

		Title:   "Wingman MCP Server",
		Version: "1.0.0",
	}

	opts := &mcp.ServerOptions{
		Instructions: instructions,

		KeepAlive: time.Second * 30,
	}

	s := mcp.NewServer(impl, opts)

	for _, t := range tools {
		// data, _ := json.Marshal(t.Schema)
		// schema := new(jsonschema.Schema)

		// if err := schema.UnmarshalJSON(data); err != nil {
		// 	return err
		// }

		// schema.Schema = "https://json-schema.org/draft/2020-12/schema"

		// if schema.Type == "object" && len(schema.Properties) == 0 {
		// 	properties := map[string]*jsonschema.Schema{}
		// 	properties["dummy_property"] = &jsonschema.Schema{
		// 		Type: "null",
		// 	}

		// 	schema.Properties = properties
		// }

		handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args map[string]any

			if v, ok := req.Params.Arguments.(json.RawMessage); ok {
				json.Unmarshal(v, &args)
			}

			result, err := t.ToolHandler(ctx, args)

			if err != nil {
				return nil, err
			}

			var content []mcp.Content

			switch v := result.(type) {
			case *mcp.CallToolResult:
				return v, nil

			case string:
				content = append(content, &mcp.TextContent{
					Text: v,
				})

			default:
				data, _ := json.Marshal(v)

				content = append(content, &mcp.TextContent{
					Text: string(data),
				})
			}

			return &mcp.CallToolResult{
				Content: content,
			}, nil
		}

		tool := &mcp.Tool{
			Name:        t.Name,
			Description: t.Description,

			InputSchema: t.Schema,
		}

		s.AddTool(tool, handler)
	}

	addr := "localhost:4200"

	mux := http.NewServeMux()

	mux.HandleFunc("GET /.well-known/wingman", func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{
			"name": "wingman",
		}

		if instructions != "" {
			data["instructions"] = instructions
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})

	sse := mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
		return s
	})

	mcp := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s
	}, nil)

	mux.Handle("/sse", sse)
	mux.Handle("/mcp", mcp)

	server := &http.Server{
		Addr:    addr,
		Handler: cors.AllowAll().Handler(mux),
	}

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}
