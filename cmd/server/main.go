package main

import (
	"context"
	"flag"

	"github.com/adrianliechti/wingman-cli/pkg/app"
	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/server"
)

func main() {
	addr := flag.String("addr", ":3000", "address to listen on")
	flag.Parse()

	cfg, cleanup, err := config.Default()

	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	defer cleanup()

	tools := cfg.Tools

	if cfg.MCP != nil {
		mcpTools, _ := cfg.MCP.Tools(context.Background())
		tools = append(tools, mcpTools...)
	}

	// Create TUI and get pre-wired server options
	ui, opts := app.NewServerUI()

	// Create server with options
	s := server.New(tools, cfg.Environment, opts)

	// Update UI with server info
	ui.SetServerInfo(*addr)

	// Start HTTP server in background
	go func() {
		if err := s.ListenAndServe(*addr); err != nil {
			panic("server error: " + err.Error())
		}
	}()

	// Run TUI (blocks until Ctrl+C)
	if err := ui.Run(); err != nil {
		panic("UI error: " + err.Error())
	}
}
