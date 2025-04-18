package agent

import (
	"context"
	_ "embed"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/cmd"
	"github.com/adrianliechti/wingman-cli/pkg/tool/fs"
	"github.com/adrianliechti/wingman-cli/pkg/util"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

var (
	//go:embed prompt_coder.txt
	prompt_coder string
)

func RunCoder(ctx context.Context, client *wingman.Client, model string) error {
	cli.Info("🤗 Hello, I'm your AI Coder")
	cli.Info()

	fs, err := fs.New("")

	if err != nil {
		return err
	}

	var tools []tool.Tool

	if t, err := fs.Tools(ctx); err == nil {
		tools = append(tools, t...)
	}

	for _, name := range []string{"git", "wget", "curl", "docker", "kubectl", "helm", "jq", "yq"} {
		if c, err := cmd.New(name); err == nil {
			println("🔨 " + name)

			t, _ := c.Tools(ctx)
			t = util.OptimizeTools(client, model, t)

			tools = append(tools, t...)
		}
	}

	cli.Info()

	return Run(ctx, client, model, tools, &RunOptions{
		Prompt: prompt_coder,
	})
}
