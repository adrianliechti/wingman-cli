package main

import (
	"context"
	"fmt"
	"os"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/app"
	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/theme"

	"github.com/adrianliechti/wingman-cli/pkg/cli/claude"
	"github.com/adrianliechti/wingman-cli/pkg/cli/codex"
	"github.com/adrianliechti/wingman-cli/pkg/cli/opencode"
)

func main() {
	ctx := context.Background()

	if len(os.Args) > 1 && os.Getenv("WINGMAN_URL") != "" {
		if os.Args[1] == "claude" {
			claude.Run(ctx, os.Args[2:])
			return
		}

		if os.Args[1] == "codex" {
			codex.Run(ctx, os.Args[2:])
			return
		}

		if os.Args[1] == "opencode" {
			opencode.Run(ctx, os.Args[2:])
			return
		}
	}

	theme.Auto()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, cleanup, err := config.Default()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	defer cleanup()

	agent := agent.New(cfg)

	app := app.New(ctx, cfg, agent)

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
