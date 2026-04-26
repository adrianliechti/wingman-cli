package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/server"
	clawtui "github.com/adrianliechti/wingman-agent/tui/claw"
	codetui "github.com/adrianliechti/wingman-agent/tui/code"

	"github.com/adrianliechti/wingman-agent/pkg/claw"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/session"
	"github.com/adrianliechti/wingman-agent/tui/proxy"

	"github.com/adrianliechti/wingman-agent/tui/run/claude"
	"github.com/adrianliechti/wingman-agent/tui/run/codex"
	"github.com/adrianliechti/wingman-agent/tui/run/gemini"
	"github.com/adrianliechti/wingman-agent/tui/run/opencode"

	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func main() {

	ctx := context.Background()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h", "help":
			printHelp(os.Stdout)
			return
		}
	}

	if len(os.Args) > 1 && os.Args[1] == "server" {
		fs := flag.NewFlagSet("server", flag.ExitOnError)
		port := fs.Int("port", 4242, "port to listen on")
		fs.Parse(os.Args[2:])

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

		s := server.New(c, *port)

		if err := s.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) > 1 && os.Getenv("WINGMAN_URL") != "" {
		if os.Args[1] == "proxy" {
			fs := flag.NewFlagSet("proxy", flag.ExitOnError)
			port := fs.Int("port", 4242, "port to listen on")
			fs.Parse(os.Args[2:])

			if err := proxy.Run(ctx, proxy.Options{Port: *port}); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		if os.Args[1] == "run" {
			if len(os.Args) > 2 {
				switch os.Args[2] {
				case "claude":
					claude.Run(ctx, os.Args[3:], nil)
					return
				case "codex":
					codex.Run(ctx, os.Args[3:], nil)
					return
				case "gemini":
					gemini.Run(ctx, os.Args[3:], nil)
					return
				case "opencode":
					opencode.Run(ctx, os.Args[3:], nil)
					return
				}
			}

			fmt.Fprintln(os.Stderr, "Error: missing or unknown run target")
			fmt.Fprintln(os.Stderr)
			printHelp(os.Stderr)
			os.Exit(1)
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

	application := codetui.New(ctx, c, sessionID)

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

func printHelp(w io.Writer) {
	fmt.Fprint(w, `wingman — AI coding agent

Usage:
  wingman [--resume [id]]      Launch the agent TUI
  wingman server [-port N]     Run the web UI server
  wingman claw                 Run the claw multi-agent runner
  wingman proxy [-port N]      Run the API proxy + dashboard (requires WINGMAN_URL)
  wingman run <target> [args]  Run an external agent through wingman (requires WINGMAN_URL)

Run targets:
  claude, codex, gemini, opencode

Flags:
  --resume [id]   Resume the latest (or specified) saved session
  --help, -h      Show this help
`)
}
