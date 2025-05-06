package mcp

import (
	"context"

	"github.com/adrianliechti/wingman-cli/app"
	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/util"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func Run(ctx context.Context, client *wingman.Client) error {
	tools := app.MustParseMCP()
	prompt := app.MustParsePrompt()

	tools = util.OptimizeTools(client, app.DefaultModel, tools)

	cli.Info()
	cli.Info("🤗 Hello, I'm your AI Assistant")
	cli.Info()

	for _, tool := range tools {
		println("🛠️ " + tool.Name)
	}

	cli.Info()

	return agent.Run(ctx, client, app.ThinkingModel, prompt, tools)
}
