package agent

import (
	"context"
	_ "embed"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/azure"
	"github.com/adrianliechti/wingman-cli/pkg/util"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

var (
	//go:embed prompt_azure.txt
	prompt_azure string
)

func RunAzure(ctx context.Context, client *wingman.Client, model string) error {
	cli.Info("🤗 Hello, I'm your Azure AI Assistant")
	cli.Info()

	azure, err := azure.New()

	if err != nil {
		return err
	}

	var tools []tool.Tool

	if t, err := azure.Tools(ctx); err == nil {
		tools = append(tools, t...)
	}

	tools = util.OptimizeTools(client, model, tools)

	cli.Info()

	return Run(ctx, client, model, tools, &RunOptions{
		Prompt:     prompt_azure,
		PromptFile: true,
	})
}
