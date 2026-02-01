package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman-cli/pkg/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/cors"
)

type Server struct {
	handler http.Handler
}

func New(tools []tool.Tool, env *tool.Environment) *Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}, nil)

	for _, t := range tools {
		addTool(mcpServer, t, env)
	}

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	corsHandler := cors.AllowAll().Handler(handler)

	return &Server{
		handler: corsHandler,
	}
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.handler)
}

func addTool(s *mcp.Server, t tool.Tool, baseEnv *tool.Environment) {
	mcpTool := &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,

		InputSchema: t.Parameters,
	}

	s.AddTool(mcpTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		env := &tool.Environment{
			Date: baseEnv.Date,

			OS:   baseEnv.OS,
			Arch: baseEnv.Arch,

			Root:    baseEnv.Root,
			Scratch: baseEnv.Scratch,

			Plan: baseEnv.Plan,
			PromptUser: func(prompt string) (bool, error) {
				return elicitConfirmation(ctx, req.Session, prompt)
			},
		}

		args := make(map[string]any)

		if req.Params.Arguments != nil {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "invalid arguments: " + err.Error()}},
					IsError: true,
				}, nil
			}
		}

		result, err := t.Execute(ctx, env, args)

		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil
	})
}

func elicitConfirmation(ctx context.Context, session *mcp.ServerSession, prompt string) (bool, error) {
	result, err := session.Elicit(ctx, &mcp.ElicitParams{
		Message: prompt,

		RequestedSchema: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Confirm the action",
				},
			},

			"required": []string{
				"confirm",
			},
		},
	})

	if err != nil {
		return true, nil
	}

	if result.Action == "accept" {
		if confirm, ok := result.Content["confirm"].(bool); ok {
			return confirm, nil
		}
	}

	return false, nil
}
