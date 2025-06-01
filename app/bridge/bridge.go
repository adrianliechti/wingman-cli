package bridge

import (
	"context"

	"github.com/adrianliechti/wingman-cli/app"
	"github.com/adrianliechti/wingman-cli/pkg/bridge"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func Run(ctx context.Context, client *wingman.Client) error {
	tools := app.MustConnectTools(ctx)

	//tools = util.OptimizeTools(client, app.DefaultModel, tools)

	cli.Info()
	cli.Info("🖥️ MCP Server")
	cli.Info()

	for _, tool := range tools {
		println("🛠️ " + tool.Name)
	}

	cli.Info()

	return bridge.Run(ctx, client, tools)
}
