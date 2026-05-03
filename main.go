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
	claudedesktop "github.com/adrianliechti/wingman-agent/tui/run/claude-desktop"
	"github.com/adrianliechti/wingman-agent/tui/run/codex"
	"github.com/adrianliechti/wingman-agent/tui/run/gemini"
	"github.com/adrianliechti/wingman-agent/tui/run/opencode"

	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if len(os.Args) < 2 {
		runTUI(ctx, "")
		return
	}

	switch os.Args[1] {
	case "--help", "-h", "help":
		printHelp(os.Stdout)
		return
	case "server":
		runServer(ctx)
		return
	case "claw":
		runClaw(ctx)
		return
	case "proxy":
		if os.Getenv("WINGMAN_URL") != "" {
			runProxy(ctx)
			return
		}
	case "run":
		runRun(ctx)
		return
	case "--resume":
		sessionID := "latest"
		if len(os.Args) > 2 {
			sessionID = os.Args[2]
		}
		runTUI(ctx, sessionID)
		return
	}

	runTUI(ctx, "")
}

func runServer(ctx context.Context) {
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

	if err := server.New(c, *port).Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runProxy(ctx context.Context) {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)
	port := fs.Int("port", 4242, "port to listen on")
	fs.Parse(os.Args[2:])

	if err := proxy.Run(ctx, proxy.Options{Port: *port}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runRun(ctx context.Context) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Error: missing or unknown run target")
		fmt.Fprintln(os.Stderr)
		printHelp(os.Stderr)
		os.Exit(1)
	}

	var err error

	switch os.Args[2] {
	case "claude":
		err = claude.Run(ctx, os.Args[3:], nil)
	case "claude-desktop":
		err = claudedesktop.Run(ctx, os.Args[3:], nil)
	case "codex":
		err = codex.Run(ctx, os.Args[3:], nil)
	case "gemini":
		err = gemini.Run(ctx, os.Args[3:], nil)
	case "opencode":
		err = opencode.Run(ctx, os.Args[3:], nil)
	default:
		fmt.Fprintln(os.Stderr, "Error: missing or unknown run target")
		fmt.Fprintln(os.Stderr)
		printHelp(os.Stderr)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runTUI(ctx context.Context, sessionID string) {
	theme.Auto()

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

	if err := codetui.New(ctx, c, sessionID).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runClaw(ctx context.Context) {
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
  wingman run <target> [args]  Run an external agent through wingman

Run targets:
  claude, claude-desktop, codex, gemini, opencode

Flags:
  --resume [id]   Resume the latest (or specified) saved session
  --help, -h      Show this help
`)
}
