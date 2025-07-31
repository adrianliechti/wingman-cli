package main

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-cli/app"
	"github.com/adrianliechti/wingman-cli/app/agent"
	"github.com/adrianliechti/wingman-cli/app/bridge"
	"github.com/adrianliechti/wingman-cli/app/browser"
	"github.com/adrianliechti/wingman-cli/app/chat"
	"github.com/adrianliechti/wingman-cli/app/coder"
	"github.com/adrianliechti/wingman-cli/app/complete"
	"github.com/adrianliechti/wingman-cli/app/rag"

	"github.com/adrianliechti/go-cli"
	"github.com/joho/godotenv"

	wingman "github.com/adrianliechti/wingman/pkg/client"
)

var version string

func main() {
	godotenv.Load()

	ctx := context.Background()

	client := app.MustClient(ctx)
	app := initApp(client)

	if err := app.Run(ctx, os.Args); err != nil {
		panic(err)
	}
}

func initApp(client *wingman.Client) cli.Command {
	return cli.Command{
		Usage: "Wingman AI CLI",

		Suggest: true,
		Version: version,

		HideHelp: true,

		HideHelpCommand: true,

		Action: func(ctx context.Context, cmd *cli.Command) error {
			prompt := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))

			if input := readInput(); input != "" {
				if prompt == "" {
					prompt += "Analyze the following input\n"
					prompt += "Explain your findings\n"
					prompt += "Give reommendations based on your observations\n"
					prompt += "If you see problems or errors, propose solutions\n"
					prompt += "\n"
					prompt += "Input:\n"
					prompt += input
				}

				return complete.Run(ctx, client, app.DefaultModel, prompt)
			}

			if cmd.Args().Len() > 0 {
				return complete.Run(ctx, client, app.DefaultModel, prompt)
			}

			return cli.ShowCommandHelp(cmd)
		},

		Commands: []*cli.Command{
			{
				Name:  "chat",
				Usage: "AI Chat",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return chat.Run(ctx, client, app.DefaultModel)
				},
			},

			{
				Name:  "rag",
				Usage: "RAG Chat",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return rag.Run(ctx, client, app.DefaultModel)
				},
			},

			{
				Name:  "agent",
				Usage: "MCP Agent",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return agent.Run(ctx, client)
				},
			},

			{
				Name:  "bridge",
				Usage: "MCP Bridge",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return bridge.Run(ctx, client)
				},
			},

			{
				Name:  "coder",
				Usage: "Code Agent",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return coder.Run(ctx, client)
				},
			},

			{
				Name:  "browser",
				Usage: "Browser Agent",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return browser.Run(ctx, client)
				},
			},
		},
	}
}

func readInput() string {
	fi, err := os.Stdin.Stat()

	if err != nil {
		return ""
	}

	if fi.Mode()&os.ModeNamedPipe == 0 {
		return ""
	}

	data, err := io.ReadAll(os.Stdin)

	if err != nil {
		return ""
	}

	return string(data)
}
