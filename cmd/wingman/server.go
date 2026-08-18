package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/server"
)

type serverCommandOptions struct {
	Host        string
	WorkDir     string
	Port        int
	PreviewPort int
	NoBrowser   bool
}

func parseServerCommand(args []string) (serverCommandOptions, error) {
	opts := serverCommandOptions{Host: "localhost"}

	fs := newFlags("wingman server")
	fs.String(&opts.Host, "--host HOST", "address to listen on (default: localhost)")
	fs.String(&opts.WorkDir, "--cd, -C PATH", "workspace to serve (default: current directory)")
	fs.Int(&opts.Port, "--port N", fmt.Sprintf("port to listen on (default: %d, falls back to random if taken)", server.DefaultPort))
	fs.Int(&opts.PreviewPort, "--preview-port N", "isolated HTML preview port (default: random)")
	fs.Bool(&opts.NoBrowser, "--no-browser", "do not open browser on startup")

	if err := fs.Parse(args); err != nil {
		return serverCommandOptions{}, err
	}

	return opts, nil
}

func runServer(ctx context.Context, args []string) {
	opts, err := parseServerCommand(args)
	if err != nil {
		fatal(err)
	}

	if opts.WorkDir == "" {
		opts.WorkDir, err = os.Getwd()
	} else {
		opts.WorkDir, err = filepath.Abs(opts.WorkDir)
	}
	if err != nil {
		fatal(fmt.Errorf("resolve workspace: %w", err))
	}

	srv, err := server.New(ctx, opts.WorkDir, &server.ServerOptions{
		Host:        opts.Host,
		PreviewPort: opts.PreviewPort,
		NoBrowser:   opts.NoBrowser,
	})
	if err != nil {
		fatal(err)
	}
	defer srv.Close()

	if err := srv.Run(ctx, opts.Port); err != nil {
		fatal(err)
	}
}
