package agent

import (
	"context"

	"github.com/adrianliechti/wingman-cli/pkg/mcp"
	"github.com/adrianliechti/wingman-cli/pkg/util"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func RunMCP(ctx context.Context, client *wingman.Client, model string) error {
	cli.Info("🤗 Hello, I'm your AI Assistant")
	cli.Info()

	cfg, err := mcp.Parse("mcp.json")

	if err != nil {
		return err
	}

	for name, _ := range cfg.Servers {
		println("🛠️ " + name)
	}

	mcp, err := mcp.New(cfg)

	if err != nil {
		return err
	}

	tools, err := mcp.Tools(ctx)

	if err != nil {
		return err
	}

	tools = util.OptimizeTools(client, model, tools)

	cli.Info()

	return Run(ctx, client, model, tools, &RunOptions{
		PromptFile: true,
	})
}
