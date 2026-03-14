package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/rs/cors"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/fs"
	"github.com/adrianliechti/wingman-cli/pkg/tool/search"
	"github.com/adrianliechti/wingman-cli/pkg/tool/shell"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func Run(ctx context.Context, opts ServerOptions) error {
	port := opts.Port

	if port == 0 {
		port = 8090
	}

	theme.Auto()

	listenAddr := fmt.Sprintf("localhost:%d", port)
	store := NewStore()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	tools, cleanup, err := loadTools()

	if err != nil {
		return fmt.Errorf("failed to load tools: %w", err)
	}

	defer cleanup()

	// Start MCP server
	errCh := make(chan error, 1)

	go func() {
		errCh <- startServer(ctx, listenAddr, tools, store)
	}()

	// Start TUI
	ui := newTUI(store, listenAddr, tools)

	go func() {
		if err := <-errCh; err != nil {
			ui.app.QueueUpdateDraw(func() {
				ui.statusBar.SetText(fmt.Sprintf("[red]Server error: %v[-]", err))
			})
		}
	}()

	return ui.Run()
}

func loadTools() ([]tool.Tool, func(), error) {
	wd, err := os.Getwd()

	if err != nil {
		return nil, nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	root, err := os.OpenRoot(wd)

	if err != nil {
		return nil, nil, err
	}

	scratchDir := filepath.Join(os.TempDir(), fmt.Sprintf("wingman-server-%d", time.Now().Unix()))

	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		root.Close()
		return nil, nil, fmt.Errorf("failed to create scratch directory: %w", err)
	}

	scratch, err := os.OpenRoot(scratchDir)

	if err != nil {
		root.Close()
		return nil, nil, fmt.Errorf("failed to open scratch directory: %w", err)
	}

	env := &tool.Environment{
		Date: time.Now().Format("January 2, 2006"),

		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,

		Root:    root,
		Scratch: scratch,
	}

	tools := slices.Concat(fs.Tools(), shell.Tools())

	if os.Getenv("WINGMAN_SEARCH") != "" {
		tools = append(tools, search.Tools()...)
	}

	// Bind environment to all tool execute functions
	wrappedTools := make([]tool.Tool, len(tools))

	for i, t := range tools {
		origExecute := t.Execute
		wrappedTools[i] = t
		wrappedTools[i].Execute = func(ctx context.Context, _ *tool.Environment, args map[string]any) (string, error) {
			return origExecute(ctx, env, args)
		}
	}

	cleanup := func() {
		scratch.Close()
		os.RemoveAll(scratchDir)
		root.Close()
	}

	return wrappedTools, cleanup, nil
}

func startServer(ctx context.Context, addr string, tools []tool.Tool, store *Store) error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}, &mcp.ServerOptions{})

	for _, t := range tools {
		registerTool(server, t, store)
	}

	crossOriginProtection := http.NewCrossOriginProtection()
	crossOriginProtection.AddInsecureBypassPattern("/{path...}")

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		CrossOriginProtection: crossOriginProtection,
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)

	corsHandler := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"*"},
		ExposedHeaders: []string{"Mcp-Session-Id"},
	}).Handler(mux)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: corsHandler,
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		httpServer.Shutdown(shutdownCtx)
	}()

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("MCP server: %w", err)
	}

	return nil
}

func registerTool(server *mcp.Server, t tool.Tool, store *Store) {
	inputSchema := t.Parameters

	if inputSchema == nil {
		inputSchema = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	mcpTool := &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: inputSchema,
	}

	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any

		if len(req.Params.Arguments) > 0 {
			json.Unmarshal(req.Params.Arguments, &args)
		}

		if args == nil {
			args = map[string]any{}
		}

		argsJSON := string(req.Params.Arguments)

		id := store.Add(ToolEntry{
			Timestamp: time.Now(),
			Tool:      t.Name,
			Arguments: argsJSON,
			Status:    "running",
		})

		start := time.Now()
		result, err := t.Execute(ctx, nil, args)
		duration := time.Since(start)

		if err != nil {
			store.Update(id, func(e *ToolEntry) {
				e.Duration = duration
				e.Error = err.Error()
				e.Status = "error"
			})

			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				},
			}, nil
		}

		store.Update(id, func(e *ToolEntry) {
			e.Duration = duration
			e.Result = result
			e.Status = "done"
		})

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: result},
			},
		}, nil
	}

	server.AddTool(mcpTool, handler)
}
