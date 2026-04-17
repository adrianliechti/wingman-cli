package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/app"
	"github.com/adrianliechti/wingman-agent/pkg/proxy"

	"github.com/adrianliechti/wingman-agent/pkg/cli/claude"
	"github.com/adrianliechti/wingman-agent/pkg/cli/codex"
	"github.com/adrianliechti/wingman-agent/pkg/cli/gemini"
	"github.com/adrianliechti/wingman-agent/pkg/cli/opencode"

	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

func main() {

	ctx := context.Background()

	if len(os.Args) > 1 && os.Getenv("WINGMAN_URL") != "" {
		if os.Args[1] == "proxy" {
			fs := flag.NewFlagSet("proxy", flag.ExitOnError)
			port := fs.Int("port", 4242, "port to listen on")
			fs.Parse(os.Args[2:])

			if err := proxy.Run(ctx, proxy.ProxyOptions{Port: *port}); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		if os.Args[1] == "claude" {
			claude.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "codex" {
			codex.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "gemini" {
			gemini.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "opencode" {
			opencode.Run(ctx, os.Args[2:], nil)
			return
		}
	}

	var sessionID string

	if len(os.Args) > 1 && os.Args[1] == "--resume" {
		if len(os.Args) > 2 {
			sessionID = os.Args[2] // wingman --resume <session-id>
		} else {
			sessionID = "latest"
		}
	}

	theme.Auto()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, cleanup, err := agent.DefaultConfig()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	defer cleanup()

	a := agent.New(cfg)

	application := app.New(ctx, a, sessionID)

	if err := application.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
