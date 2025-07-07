package rag

import (
	"context"
	_ "embed"
	"path/filepath"

	"github.com/adrianliechti/go-cli"
	"github.com/adrianliechti/wingman-cli/app"
	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/index"
	"github.com/adrianliechti/wingman-cli/pkg/tool/retriever"

	wingman "github.com/adrianliechti/wingman/pkg/client"
)

var (
	//go:embed prompt.txt
	DefaultPrompt string
)

func Run(ctx context.Context, client *wingman.Client, model string) error {
	cli.Info()
	cli.Info("🤗 Hello, I'm your RAG")
	cli.Info()

	root := app.MustDir()

	instructions := app.MustParseInstructions()

	if instructions == "" {
		instructions = DefaultPrompt
	}

	resources := app.MustConnectResources(ctx)

	index, err := index.New(filepath.Join(root, "wingman.db"), &embeder{client})

	if err != nil {
		return err
	}

	if err := IndexDir(ctx, client, index, root); err != nil {
		return err
	}

	if err := IndexResources(ctx, client, index, resources); err != nil {
		return err
	}

	cli.Info()

	tools, err := retriever.New(index).Tools(ctx)

	if err != nil {
		return err
	}

	return agent.Run(ctx, client, model, instructions, tools)
}
