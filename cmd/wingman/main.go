package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
)

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if len(os.Args) < 2 {
		runTUI(ctx, tuiOptions{Agent: "wingman"})
		return
	}

	switch os.Args[1] {
	case "--help", "-h", "help":
		printHelp(os.Stdout)
		return
	case "server":
		runServer(ctx)
		return
	case "acp":
		runACP(ctx)
		return
	case "claw":
		runClaw(ctx)
		return
	case "proxy":
		runProxy(ctx)
		return
	case "run":
		runRun(ctx)
		return
	case "--resume", "--agent", "-a":
		opts, err := parseTUIArgs(os.Args[1:])
		if err != nil {
			fatal(err)
		}
		runTUI(ctx, opts)
		return
	default:
		fatal(fmt.Errorf("unknown command %q (run 'wingman --help' for usage)", os.Args[1]))
	}
}

func parseTUIArgs(args []string) (tuiOptions, error) {
	opts := tuiOptions{Agent: "wingman"}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent", "-a":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return tuiOptions{}, fmt.Errorf("%s requires an agent name", args[i])
			}
			i++
			opts.Agent = args[i]

		case "--resume":
			opts.SessionID = "latest"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				opts.SessionID = args[i]
			}

		default:
			return tuiOptions{}, fmt.Errorf("unknown TUI option %q", args[i])
		}
	}

	return opts, nil
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `wingman — AI coding agent

Usage:
  wingman [--agent name] [--resume [id]]  Launch the agent TUI
  wingman server [-port N] [-no-browser]  Run the web UI server
  wingman acp [target]         Run as an ACP stdio server (wingman | claude | codex | pi)
  wingman claw [--plain]      Run the claw multi-agent runner (TUI; plain REPL when piped or with --plain)
  wingman proxy [-port N]      Run the API proxy + dashboard (requires WINGMAN_URL)
  wingman run <target> [args]  Run an external agent through wingman

Run targets:
  claude, claude-desktop, codex, copilot, gemini, goose, junie, opencode, pi

Flags:
  --agent, -a name  Use wingman or any detected/configured agent (default: wingman)
  --resume [id]    Resume that agent's latest (or specified) session
  --help, -h       Show this help
`)
}
