package proxy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func Run(ctx context.Context, opts ProxyOptions) error {
	upstream := opts.URL
	if upstream == "" {
		upstream = os.Getenv("WINGMAN_URL")
	}
	upstream = strings.TrimRight(upstream, "/")

	token := opts.Token
	if token == "" {
		token = os.Getenv("WINGMAN_TOKEN")
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
		errCh <- startServer(ctx, listenAddr, upstream, token, opts.User, store)
	}()

	// Start TUI
	ui := newTUI(store, listenAddr, upstream)

	go func() {
		if err := <-errCh; err != nil {
			ui.app.QueueUpdateDraw(func() {
				ui.statusBar.SetText(fmt.Sprintf("[red]Server error: %v[-]", err))
			})
		}
	}()

	if err := ui.Run(); err != nil {
		cancel()
		return err
	}

	cancel()
	return nil
}
