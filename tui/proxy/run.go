package proxy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/proxy"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type Options struct {
	Port  int
	URL   string
	Token string

	User *proxy.UserInfo
}

func Run(ctx context.Context, opts Options) error {
	if opts.Token == "" {
		opts.Token = os.Getenv("WINGMAN_TOKEN")
	}

	if opts.URL == "" {
		opts.URL = os.Getenv("WINGMAN_URL")
	}

	if opts.Port == 0 {
		opts.Port = 4242
	}

	theme.Auto()

	p := proxy.New(proxy.Config{
		Addr:     fmt.Sprintf("localhost:%d", opts.Port),
		Upstream: strings.TrimRight(opts.URL, "/"),
		Token:    opts.Token,
		User:     opts.User,
	})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)

	go func() {
		errCh <- p.Start(ctx)
	}()

	ui := newApp(p)

	go func() {
		if err := <-errCh; err != nil {
			ui.app.QueueUpdateDraw(func() {
				ui.statusBar.SetText(fmt.Sprintf("[red]Server error: %v[-]", err))
			})
		}
	}()

	return ui.Run()
}
