package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/adrianliechti/wingman/pkg/chat"
	"github.com/adrianliechti/wingman/pkg/cli"
	"github.com/adrianliechti/wingman/pkg/coder"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

var version string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM)
	defer stop()

	app := initApp()

	if err := app.Run(ctx, os.Args); err != nil {
		cli.Fatal(err)
	}
}

func initApp() cli.Command {
	client := openai.NewClient(
		option.WithBaseURL("http://localhost:8080/v1/"),
		option.WithAPIKey("-"),
	)

	model := openai.ChatModelGPT4o

	return cli.Command{
		Usage: "Wingman AI CLI",

		Suggest: true,
		Version: version,

		HideHelpCommand: true,

		Commands: []*cli.Command{
			{
				Name:  "chat",
				Usage: "AI Chat",

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return chat.Run(ctx, client, model)
				},
			},
			{
				Name:  "coder",
				Usage: "AI Coder",

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return coder.Run(ctx, client, model, "")
				},
			},
		},
	}
}
