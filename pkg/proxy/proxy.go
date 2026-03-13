package proxy

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func Run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)

	port := fs.Int("port", 4242, "port to listen on")
	upstream := fs.String("upstream", "", "upstream OpenAI-compatible API URL (required)")

	fs.Parse(args)

	if *upstream == "" {
		fmt.Fprintln(os.Stderr, "Error: --upstream is required")
		fmt.Fprintln(os.Stderr, "Usage: wingman proxy --upstream https://api.openai.com --port 4242")
		os.Exit(1)
	}

	theme.Auto()

	listenAddr := fmt.Sprintf("localhost:%d", *port)
	store := NewStore()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start HTTP proxy server
	errCh := make(chan error, 1)

	go func() {
		errCh <- startServer(ctx, listenAddr, *upstream, store)
	}()

	// Start TUI
	ui := newTUI(store, listenAddr, *upstream)

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
