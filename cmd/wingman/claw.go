package main

import (
	"context"
	"os"

	"golang.org/x/term"

	"github.com/adrianliechti/wingman-agent/pkg/claw"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel/cli"
	clawtui "github.com/adrianliechti/wingman-agent/pkg/tui/claw"
)

func runClaw(ctx context.Context, args []string) {
	var plain bool

	fs := newFlags("wingman claw")
	fs.Bool(&plain, "--plain", "plain REPL output (automatic when piped)")

	if err := fs.Parse(args); err != nil {
		fatal(err)
	}

	if !plain {
		plain = !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd()))
	}

	cfg, cleanup, err := claw.DefaultConfig()
	if err != nil {
		fatal(err)
	}
	defer cleanup()

	c := claw.New(cfg)

	if err := c.Init(); err != nil {
		fatal(err)
	}
	defer c.Close()

	if plain {
		cfg.Channels = []channel.Channel{cli.New()}
	} else {
		cfg.Channels = []channel.Channel{clawtui.New(c)}
	}

	if err := c.Run(ctx); err != nil {
		fatal(err)
	}
}
