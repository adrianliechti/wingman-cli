package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
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
		KeepAlive: time.Second * 30,
	}

	s := mcp.NewServer(impl, opts)

	for _, t := range tools {
		data, _ := json.Marshal(t.Schema)
		schema := new(jsonschema.Schema)

		if err := schema.UnmarshalJSON(data); err != nil {
			return err
		}

		if schema.Type == "object" && len(schema.Properties) == 0 {
			properties := map[string]*jsonschema.Schema{}
			properties["dummy_property"] = &jsonschema.Schema{
				Type: "null",
			}

			schema.Properties = properties
		}

		handler := func(ctx context.Context, session *mcp.ServerSession, params *mcp.CallToolParamsFor[map[string]any]) (*mcp.CallToolResultFor[any], error) {
			args := params.Arguments

			result, err := t.Execute(ctx, args)

			if err != nil {
				return &mcp.CallToolResultFor[any]{
					IsError: true,

					Content: []mcp.Content{
						&mcp.TextContent{
							Text: err.Error(),
						},
					},
				}, nil
			}

			var content string

			switch v := result.(type) {
			case string:
				content = v
			default:
				data, _ := json.Marshal(v)
				content = string(data)
			}

			return &mcp.CallToolResultFor[any]{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: content,
					},
				},
			}, nil
		}

		tool := &mcp.Tool{
			Name:        t.Name,
			Description: t.Description,

			InputSchema: schema,
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

	h := mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
		return s
	})

	mux.Handle("/sse", h)
	// mux.Handle("/message", h)

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
