package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/tui/proxy"
)

func runProxy(ctx context.Context, args []string) {
	port := 4242

	fs := newFlags("wingman proxy")
	fs.Int(&port, "--port N", "port to listen on")

	if err := fs.Parse(args); err != nil {
		fatal(err)
	}

	if strings.TrimSpace(os.Getenv("WINGMAN_URL")) == "" {
		fatal(fmt.Errorf("wingman proxy requires WINGMAN_URL"))
	}

	if err := proxy.Run(ctx, proxy.Options{Port: port}); err != nil {
		fatal(err)
	}
}
