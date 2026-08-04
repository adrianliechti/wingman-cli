package main

import (
	"context"
	"fmt"
	"os"

	"github.com/adrianliechti/wingman-agent/server"
)

func runServer(ctx context.Context, args []string) {
	var port int
	var noBrowser bool

	fs := newFlags("wingman server")
	fs.Int(&port, "--port N", fmt.Sprintf("port to listen on (default: %d, falls back to random if taken)", server.DefaultPort))
	fs.Bool(&noBrowser, "--no-browser", "do not open browser on startup")

	if err := fs.Parse(args); err != nil {
		fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	srv, err := server.New(ctx, wd, &server.ServerOptions{
		NoBrowser: noBrowser,
	})
	if err != nil {
		fatal(err)
	}
	defer srv.Close()

	if err := srv.Run(ctx, port); err != nil {
		fatal(err)
	}
}
