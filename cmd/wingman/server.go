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
	remoteURL := os.Getenv("WINGMAN_REMOTE_URL")
	remoteToken := os.Getenv("WINGMAN_REMOTE_TOKEN")

	fs := newFlags("wingman server")
	fs.Int(&port, "--port N", fmt.Sprintf("port to listen on (default: %d, falls back to random if taken)", server.DefaultPort))
	fs.Bool(&noBrowser, "--no-browser", "do not open browser on startup")
	fs.String(&remoteURL, "--remote URL", "connect through a Wingman relay (or WINGMAN_REMOTE_URL)")
	fs.String(&remoteToken, "--remote-token TOKEN", "relay registration token (or WINGMAN_REMOTE_TOKEN)")

	if err := fs.Parse(args); err != nil {
		fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	srv, err := server.New(ctx, wd, &server.ServerOptions{
		NoBrowser:   noBrowser,
		RemoteURL:   remoteURL,
		RemoteToken: remoteToken,
	})
	if err != nil {
		fatal(err)
	}
	defer srv.Close()

	if err := srv.Run(ctx, port); err != nil {
		fatal(err)
	}
}
