package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/app"
	"github.com/adrianliechti/wingman-agent/app/session"

	"github.com/adrianliechti/wingman-agent/pkg/claw"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
	clawtui "github.com/adrianliechti/wingman-agent/pkg/claw/tui"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/proxy"

	"github.com/adrianliechti/wingman-agent/pkg/cli/claude"
	"github.com/adrianliechti/wingman-agent/pkg/cli/codex"
	"github.com/adrianliechti/wingman-agent/pkg/cli/gemini"
	"github.com/adrianliechti/wingman-agent/pkg/cli/junie"
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

		if os.Args[1] == "codex" {
			codex.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "claude" {
			claude.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "gemini" {
			gemini.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "junie" {
			junie.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "opencode" {
			opencode.Run(ctx, os.Args[2:], nil)
			return
		}

	}

	if len(os.Args) > 1 && os.Args[1] == "claw" {
		runClaw(ctx)
		return
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

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	c, err := code.New(wd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	if sessionID == "latest" {
		sessions, err := session.List(filepath.Join(filepath.Dir(c.MemoryPath), "sessions"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if len(sessions) > 0 {
			sessionID = sessions[0].ID
		} else {
			sessionID = ""
		}
	}

	if sessionID != "" {
		s, err := session.Load(filepath.Join(filepath.Dir(c.MemoryPath), "sessions"), sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		c.Messages = s.State.Messages
		c.Usage = s.State.Usage
	}

	application := app.New(ctx, c, sessionID)

	if err := application.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runClaw(ctx context.Context) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	cfg, cleanup, err := claw.DefaultConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	c := claw.New(cfg)

	if err := c.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Replace CLI channel with TUI
	cfg.Channels = []channel.Channel{clawtui.New(c)}

	if err := c.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
