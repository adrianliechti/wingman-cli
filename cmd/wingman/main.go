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

	args := os.Args[1:]

	if len(args) == 0 {
		runTUI(ctx, tuiOptions{Agent: "wingman"})
		return
	}

	switch args[0] {
	case "--help", "-h", "help":
		printHelp(os.Stdout)
	case "exec", "e":
		if err := runExec(ctx, args[1:]); err != nil {
			fatal(err)
		}
	case "server":
		runServer(ctx, args[1:])
	case "acp":
		runACP(ctx, args[1:])
	case "run":
		runRun(ctx, args[1:])
	default:
		if !strings.HasPrefix(args[0], "-") {
			fatal(fmt.Errorf("unknown command %q (run 'wingman --help' for usage)", args[0]))
		}

		opts, err := parseTUIArgs(args)

		if err != nil {
			fatal(err)
		}

		runTUI(ctx, opts)
	}
}

func parseTUIArgs(args []string) (tuiOptions, error) {
	opts := tuiOptions{Agent: "wingman"}

	var latest bool

	fs := newFlags("wingman")
	fs.String(&opts.Agent, "--agent, -a name", "use wingman or any detected/configured agent")
	fs.Bool(&latest, "--continue, -c", "resume the agent's latest session")
	fs.String(&opts.SessionID, "--resume, -r ID", "resume the specified session")

	if err := fs.Parse(args); err != nil {
		return tuiOptions{}, err
	}

	if latest && opts.SessionID == "" {
		opts.SessionID = "latest"
	}

	return opts, nil
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `wingman — AI coding agent

Usage:
  wingman [flags]               Launch the agent TUI
  wingman exec [flags] [PROMPT] Run non-interactively (alias: e)
  wingman exec resume [ID]      Resume a non-interactive session
  wingman server [flags]        Run the web UI server
  wingman acp [target] [flags]  Run as an ACP stdio server (wingman | claude | codex | pi)
  wingman run <target> [args]   Run an external agent through wingman

Run targets:
  claude, claude-desktop, codex, copilot, gemini, goose, junie, opencode, pi

TUI flags:
  --agent, -a name  Use wingman or any detected/configured agent (default: wingman)
  --continue, -c    Resume the agent's latest session
  --resume, -r ID   Resume the specified session
  --help, -h        Show this help

Exec flags:
  --json                        Return the final response as a JSON object
  --debug                       Print reasoning and tool details to stderr
  --schema PATH                 Require final JSON matching a JSON Schema
  --ephemeral                   Delete the session after the run
  --model, -m MODEL             Override the model for this session
  --effort LEVEL                Override the reasoning effort for this session
  --agent, -a NAME              Use wingman or any detected/configured agent
  --cd, -C PATH                 Set the workspace root

Resume:
  wingman exec resume --last [PROMPT]
  wingman exec resume SESSION_ID [PROMPT]

Run 'wingman <command> --help' for command flags.
`)
}
