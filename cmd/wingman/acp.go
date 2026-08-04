package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/acp/claude"
	"github.com/adrianliechti/wingman-agent/pkg/acp/codex"
	"github.com/adrianliechti/wingman-agent/pkg/acp/pi"
	"github.com/adrianliechti/wingman-agent/pkg/acp/server"
	claudecli "github.com/adrianliechti/wingman-agent/pkg/external/claude"
	codexcli "github.com/adrianliechti/wingman-agent/pkg/external/codex"
	picli "github.com/adrianliechti/wingman-agent/pkg/external/pi"
)

type acpBackend string

const (
	acpBackendNative  acpBackend = "native"
	acpBackendWingman acpBackend = "wingman"
)

func parseACPBackend(value string) (acpBackend, error) {
	switch acpBackend(strings.ToLower(strings.TrimSpace(value))) {
	case acpBackendNative:
		return acpBackendNative, nil
	case acpBackendWingman:
		return acpBackendWingman, nil
	default:
		return "", fmt.Errorf("unknown ACP backend %q (choose native or wingman)", value)
	}
}

func printACPHelp() {
	fmt.Fprint(os.Stdout, `Usage:
  wingman acp [wingman]
  wingman acp claude [--backend native|wingman] [--model ID] [--effort LEVEL]
  wingman acp codex  [--backend native|wingman] [--model ID] [--effort LEVEL]
  wingman acp pi     [--backend native|wingman]

The native backend reuses the agent's existing configuration and login.
The wingman backend routes model traffic through WINGMAN_URL (or localhost:4242).
`)
}

func runACP(ctx context.Context) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	target := "wingman"
	if len(os.Args) >= 3 {
		target = os.Args[2]
	}

	switch target {
	case "--help", "-h", "help":
		printACPHelp()
	case "wingman":
		if len(os.Args) > 3 {
			if os.Args[3] == "--help" || os.Args[3] == "-h" {
				printACPHelp()
				return
			}
			fatal(fmt.Errorf("wingman acp wingman does not accept arguments"))
		}
		if err := server.Run(ctx, os.Stdin, os.Stdout); err != nil {
			fatal(err)
		}
	case "claude":
		runACPClaude(ctx)
	case "codex":
		runACPCodex(ctx)
	case "pi":
		runACPPi(ctx)
	default:
		fatal(fmt.Errorf("unknown ACP target %q (choose wingman, claude, codex, or pi)", target))
	}
}

func runACPClaude(ctx context.Context) {
	fs := flag.NewFlagSet("acp claude", flag.ExitOnError)
	model := fs.String("model", "default", "default model id for new sessions")
	effort := fs.String("effort", "", "default effort level (low|medium|high|xhigh|max)")
	backendFlag := fs.String("backend", string(acpBackendNative), "model backend (native|wingman)")
	debug := fs.Bool("debug", false, "log JSON-RPC traffic to stderr")
	fs.Parse(os.Args[3:])
	backend, err := parseACPBackend(*backendFlag)
	if err != nil {
		fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	opts := claude.Options{
		Model:  *model,
		Effort: *effort,
		Cwd:    cwd,
		Env:    os.Environ(),
	}
	if backend == acpBackendWingman {
		cfg, err := claudecli.NewConfig(ctx, nil)
		if err != nil {
			fatal(err)
		}
		opts.Env = claudecli.BuildEnv(os.Environ(), cfg)
	}

	if err := claude.Run(ctx, opts, os.Stdin, os.Stdout, acpLogger(*debug)); err != nil {
		fatal(err)
	}
}

func runACPCodex(ctx context.Context) {
	fs := flag.NewFlagSet("acp codex", flag.ExitOnError)
	model := fs.String("model", "default", "default model id for new sessions")
	effort := fs.String("effort", "", "default reasoning effort (minimal|low|medium|high|xhigh)")
	backendFlag := fs.String("backend", string(acpBackendNative), "model backend (native|wingman)")
	debug := fs.Bool("debug", false, "log JSON-RPC traffic to stderr")
	fs.Parse(os.Args[3:])
	backend, err := parseACPBackend(*backendFlag)
	if err != nil {
		fatal(err)
	}

	opts := codex.Options{
		Model:  *model,
		Effort: *effort,
		Env:    os.Environ(),
	}
	if backend == acpBackendWingman {
		cfg, err := codexcli.NewConfig(ctx, nil)
		if err != nil {
			fatal(err)
		}
		opts.Env = codexcli.BuildEnv(os.Environ(), cfg)
		opts.ExtraArgs = codexcli.BuildArgs(cfg)
	}

	if err := codex.Run(ctx, opts, os.Stdin, os.Stdout, acpLogger(*debug)); err != nil {
		fatal(err)
	}
}

func runACPPi(ctx context.Context) {
	fs := flag.NewFlagSet("acp pi", flag.ExitOnError)
	backendFlag := fs.String("backend", string(acpBackendNative), "model backend (native|wingman)")
	debug := fs.Bool("debug", false, "log JSON-RPC traffic to stderr")
	fs.Parse(os.Args[3:])
	backend, err := parseACPBackend(*backendFlag)
	if err != nil {
		fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	opts := pi.Options{
		Path:        picli.BinPath(),
		Dir:         cwd,
		Env:         os.Environ(),
		SessionsDir: picli.NativeSessionsDir(),
	}
	if backend == acpBackendWingman {
		cfg, err := picli.NewConfig(ctx, nil)
		if err != nil {
			fatal(err)
		}
		dir, err := picli.ConfigDir()
		if err != nil {
			fatal(err)
		}
		if err := picli.WriteModels(dir, cfg); err != nil {
			fatal(err)
		}
		opts.Env = picli.BuildEnv(os.Environ(), dir)
		opts.Args = picli.BuildArgs(cfg)
		opts.SessionsDir = picli.SessionsDir(dir)
	}

	if err := pi.Run(ctx, opts, os.Stdin, os.Stdout, acpLogger(*debug)); err != nil {
		fatal(err)
	}
}

func acpLogger(debug bool) *slog.Logger {
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
