package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman-cli/pkg/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/cors"
)

// Options configures the server with optional hooks
type Options struct {
	// OnToolStart is called when a tool starts executing
	OnToolStart func(ctx context.Context, name string, args string)

	// OnToolComplete is called when a tool completes successfully
	OnToolComplete func(ctx context.Context, name string, args string, result string)

	// OnToolError is called when a tool fails
	OnToolError func(ctx context.Context, name string, args string, err error)

	// OnPromptUser is called to prompt the user for confirmation
	// If nil, defaults to auto-approve
	OnPromptUser func(ctx context.Context, prompt string) (bool, error)
}

type Server struct {
	handler http.Handler
	opts    *Options
}

func New(tools []tool.Tool, env *tool.Environment, opts *Options) *Server {
	if opts == nil {
		opts = &Options{}
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}, nil)

	for _, t := range tools {
		addTool(mcpServer, t, env, opts)
	}

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	corsHandler := cors.AllowAll().Handler(handler)

	return &Server{
		handler: corsHandler,
		opts:    opts,
	}
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.handler)
}

func addTool(s *mcp.Server, t tool.Tool, baseEnv *tool.Environment, opts *Options) {
	mcpTool := &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,

		InputSchema: t.Parameters,
	}

	s.AddTool(mcpTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := make(map[string]any)

		if req.Params.Arguments != nil {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "invalid arguments: " + err.Error()}},
					IsError: true,
				}, nil
			}
		}

		argsJSON, _ := json.Marshal(args)

		// Call OnToolStart hook
		if opts.OnToolStart != nil {
			opts.OnToolStart(ctx, t.Name, string(argsJSON))
		}

		env := &tool.Environment{
			Date: baseEnv.Date,

			OS:   baseEnv.OS,
			Arch: baseEnv.Arch,

			Root:    baseEnv.Root,
			Scratch: baseEnv.Scratch,

			Plan: baseEnv.Plan,
			PromptUser: func(prompt string) (bool, error) {
				if opts.OnPromptUser != nil {
					return opts.OnPromptUser(ctx, prompt)
				}
				// Auto-approve if no handler set
				return true, nil
			},
		}

		result, err := t.Execute(ctx, env, args)

		if err != nil {
			// Call OnToolError hook
			if opts.OnToolError != nil {
				opts.OnToolError(ctx, t.Name, string(argsJSON), err)
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}

		// Call OnToolComplete hook
		if opts.OnToolComplete != nil {
			opts.OnToolComplete(ctx, t.Name, string(argsJSON), result)
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil
	})
}
