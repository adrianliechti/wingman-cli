package agent

import (
	"context"
	_ "embed"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/azure"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

var (
	//go:embed system_azure.txt
	system_azure string
)

func RunAzure(ctx context.Context, client *wingman.Client, model string) error {
	println("🤗 Hello, I'm your Azure AI Assistant")
	println()

	azure, err := azure.New()

	if err != nil {
		return err
	}

	system, err := ParsePrompt()

	if err != nil {
		return err
	}

	if system == "" {
		system = system_azure
	}

	var tools []tool.Tool

	if t, err := azure.Tools(ctx); err == nil {
		tools = append(tools, t...)
	}

	tools = toolsWrapper(client, model, tools)

	println()

	return Run(ctx, client, model, tools, &RunOptions{
		System: system,
	})
}
