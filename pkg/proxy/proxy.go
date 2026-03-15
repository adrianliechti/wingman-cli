package proxy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/theme"
)

func Run(ctx context.Context, opts ProxyOptions) error {
	token := opts.Token
	target := strings.TrimRight(opts.URL, "/")

	if token == "" {
		token = os.Getenv("WINGMAN_TOKEN")
	}

	if target == "" {
		target = os.Getenv("WINGMAN_URL")
	}

	port := opts.Port

	if port == 0 {
		port = 4242
	}

	theme.Auto()

	listenAddr := fmt.Sprintf("localhost:%d", port)
	store := NewStore()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start HTTP proxy server
	errCh := make(chan error, 1)

	go func() {
		errCh <- startServer(ctx, listenAddr, target, token, opts.User, store)
	}()

	// Start TUI
	ui := newTUI(store, listenAddr, target)

	go func() {
		if err := <-errCh; err != nil {
			ui.app.QueueUpdateDraw(func() {
				ui.statusBar.SetText(fmt.Sprintf("[red]Server error: %v[-]", err))
			})
		}
	}()

	return ui.Run()
}
