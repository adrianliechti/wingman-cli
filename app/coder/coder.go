package coder

import (
	"context"
	_ "embed"

	"github.com/adrianliechti/wingman-cli/app"
	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/cmd"
	"github.com/adrianliechti/wingman-cli/pkg/tool/fs"
	"github.com/adrianliechti/wingman-cli/pkg/util"

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

	cli.Info()
	cli.Info("🤗 Hello, I'm your AI Coder")
	cli.Info()

	var tools []tool.Tool

	if t, err := fs.Tools(ctx); err == nil {
		tools = append(tools, t...)
	}

	for _, name := range []string{"git", "wget", "curl", "docker", "kubectl", "helm", "jq", "yq"} {
		if c, err := cmd.New(name); err == nil {
			t, _ := c.Tools(ctx)
			t = util.OptimizeTools(client, app.DefaultModel, t)

			tools = append(tools, t...)
		}
	}

	return agent.Run(ctx, client, app.ThinkingModel, DefaultPrompt, tools)
}
