package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	acpclaude "github.com/adrianliechti/wingman-agent/pkg/acp/claude"
	acpcodex "github.com/adrianliechti/wingman-agent/pkg/acp/codex"
	acppi "github.com/adrianliechti/wingman-agent/pkg/acp/pi"
	acpserver "github.com/adrianliechti/wingman-agent/pkg/acp/server"
	extclaude "github.com/adrianliechti/wingman-agent/pkg/external/claude"
	extcodex "github.com/adrianliechti/wingman-agent/pkg/external/codex"
	extpi "github.com/adrianliechti/wingman-agent/pkg/external/pi"
)

func runACP(ctx context.Context) {

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if len(os.Args) >= 3 {
		switch os.Args[2] {
		case "claude":
			runACPClaude(ctx)
			return
		case "codex":
			runACPCodex(ctx)
			return
		case "pi":
			runACPPi(ctx)
			return
		}
	}

	if err := acpserver.Run(ctx, os.Stdin, os.Stdout); err != nil {
		fatal(err)
	}
}

func runACPClaude(ctx context.Context) {
	fs := flag.NewFlagSet("acp claude", flag.ExitOnError)
	model := fs.String("model", "default", "default model id for new sessions")
	effort := fs.String("effort", "", "default effort level (low|medium|high|xhigh|max)")
	debug := fs.Bool("debug", false, "log JSON-RPC traffic to stderr")
	fs.Parse(os.Args[3:])

	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	cfg, err := extclaude.NewConfig(ctx, nil)
	if err != nil {
		fatal(err)
	}

	opts := acpclaude.Options{
		Model:  *model,
		Effort: *effort,
		Cwd:    cwd,
		Env:    extclaude.BuildEnv(os.Environ(), cfg),
	}

	if err := acpclaude.Run(ctx, opts, os.Stdin, os.Stdout, acpLogger(*debug)); err != nil {
		fatal(err)
	}
}

func runACPCodex(ctx context.Context) {
	fs := flag.NewFlagSet("acp codex", flag.ExitOnError)
	model := fs.String("model", "default", "default model id for new sessions")
	effort := fs.String("effort", "", "default reasoning effort (minimal|low|medium|high|xhigh)")
	debug := fs.Bool("debug", false, "log JSON-RPC traffic to stderr")
	fs.Parse(os.Args[3:])

	cfg, err := extcodex.NewConfig(ctx, nil)
	if err != nil {
		fatal(err)
	}

	opts := acpcodex.Options{
		Model:     *model,
		Effort:    *effort,
		Env:       extcodex.BuildEnv(os.Environ(), cfg),
		ExtraArgs: extcodex.BuildArgs(cfg),
	}

	if err := acpcodex.Run(ctx, opts, os.Stdin, os.Stdout, acpLogger(*debug)); err != nil {
		fatal(err)
	}
}

func runACPPi(ctx context.Context) {
	fs := flag.NewFlagSet("acp pi", flag.ExitOnError)
	debug := fs.Bool("debug", false, "log JSON-RPC traffic to stderr")
	fs.Parse(os.Args[3:])

	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	cfg, err := extpi.NewConfig(ctx, nil)
	if err != nil {
		fatal(err)
	}

	dir, err := extpi.ConfigDir()
	if err != nil {
		fatal(err)
	}

	if err := extpi.WriteModels(dir, cfg); err != nil {
		fatal(err)
	}

	opts := acppi.Options{
		Path:        extpi.BinPath(),
		Dir:         cwd,
		Env:         extpi.BuildEnv(os.Environ(), dir),
		Args:        extpi.BuildArgs(cfg),
		SessionsDir: extpi.SessionsDir(dir),
	}

	if err := acppi.Run(ctx, opts, os.Stdin, os.Stdout, acpLogger(*debug)); err != nil {
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
