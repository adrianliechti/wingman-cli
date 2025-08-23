package powerups

import (
	"context"
	_ "embed"

	"github.com/adrianliechti/wingman-cli/pkg/bridge"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/browser"
	"github.com/adrianliechti/wingman-cli/pkg/tool/duckduckgo"
	"github.com/adrianliechti/wingman-cli/pkg/tool/fs"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

var (
	//go:embed prompt.txt
	DefaultPrompt string
)

func Run(ctx context.Context, client *wingman.Client) error {
	fs, err := fs.New("")

	if err != nil {
		return err
	}

	ddg, err := duckduckgo.New()

	if err != nil {
		return err
	}

	browser, err := browser.New()

	if err != nil {
		return err
	}

	defer browser.Close()

	cli.Info()
	cli.Info("Wingman Power-Ups")
	cli.Info()

	var tools []tool.Tool

	if t, err := fs.Tools(ctx); err == nil {
		tools = append(tools, t...)
	}

	if t, err := ddg.Tools(ctx); err == nil {
		tools = append(tools, t...)
	}

	if t, err := browser.Tools(ctx); err == nil {
		tools = append(tools, t...)
	}

	return bridge.Run(ctx, client, "", tools)
}
