package main

import (
	"context"
	"fmt"
	"os"

	"github.com/adrianliechti/wingman-agent/pkg/external/claude"
	"github.com/adrianliechti/wingman-agent/pkg/external/claude-desktop"
	"github.com/adrianliechti/wingman-agent/pkg/external/codex"
	"github.com/adrianliechti/wingman-agent/pkg/external/copilot"
	"github.com/adrianliechti/wingman-agent/pkg/external/gemini"
	"github.com/adrianliechti/wingman-agent/pkg/external/goose"
	"github.com/adrianliechti/wingman-agent/pkg/external/junie"
	"github.com/adrianliechti/wingman-agent/pkg/external/opencode"
	"github.com/adrianliechti/wingman-agent/pkg/external/pi"
)

func runRun(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: missing or unknown run target")
		fmt.Fprintln(os.Stderr)
		printHelp(os.Stderr)
		os.Exit(1)
	}

	var err error

	switch args[0] {
	case "claude":
		err = claude.Run(ctx, args[1:], nil)
	case "claude-desktop":
		err = claudedesktop.Run(ctx, args[1:], nil)
	case "codex":
		err = codex.Run(ctx, args[1:], nil)
	case "copilot":
		err = copilot.Run(ctx, args[1:], nil)
	case "gemini":
		err = gemini.Run(ctx, args[1:], nil)
	case "goose":
		err = goose.Run(ctx, args[1:], nil)
	case "junie":
		err = junie.Run(ctx, args[1:], nil)
	case "opencode":
		err = opencode.Run(ctx, args[1:], nil)
	case "pi":
		err = pi.Run(ctx, args[1:], nil)
	default:
		fmt.Fprintln(os.Stderr, "Error: missing or unknown run target")
		fmt.Fprintln(os.Stderr)
		printHelp(os.Stderr)
		os.Exit(1)
	}

	if err != nil {
		fatal(err)
	}
}
