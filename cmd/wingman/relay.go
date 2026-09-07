package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/adrianliechti/wingman-agent/pkg/remote"
)

func runRelay(ctx context.Context, args []string) {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer cancel()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	port := 8080
	token := os.Getenv("WINGMAN_RELAY_TOKEN")

	fs := newFlags("wingman relay")
	fs.Int(&port, "--port N", "HTTP port behind your gateway (default: 8080)")
	fs.String(&token, "--token TOKEN", "workspace registration token (or WINGMAN_RELAY_TOKEN)")

	if err := fs.Parse(args); err != nil {
		fatal(err)
	}

	if token == "" {
		fatal(fmt.Errorf("set --token or WINGMAN_RELAY_TOKEN to authorize workspace servers"))
		return
	}
	fmt.Fprintf(os.Stderr, "Wingman relay listening on :%d\n", port)

	if err := remote.NewRelay(token).ListenAndServe(ctx, port); err != nil {
		fatal(err)
	}
}
