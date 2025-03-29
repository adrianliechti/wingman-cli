package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/adrianliechti/wingman-cli/pkg/admin"
	"github.com/adrianliechti/wingman-cli/pkg/chat"
	"github.com/adrianliechti/wingman-cli/pkg/cli"
	"github.com/adrianliechti/wingman-cli/pkg/coder"
	"github.com/adrianliechti/wingman-cli/pkg/completer"
	"github.com/adrianliechti/wingman-cli/pkg/openapi"

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
	apiKey := os.Getenv("OPENAI_API_KEY")

	if apiKey == "" {
		apiKey = "-"
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")

	if baseURL == "" {
		baseURL = "https://api.openai.com/v1/"

		if apiKey == "-" {
			baseURL = "http://localhost:8080/v1/"
		}
	}

	defaultModel := os.Getenv("OPENAI_MODEL")

	if defaultModel == "" {
		defaultModel = openai.ChatModelGPT4o
	}

	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(strings.TrimRight(baseURL, "/")+"/"),
	)

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

				return completer.Run(ctx, client, defaultModel, prompt)
			}

			if cmd.Args().Len() > 0 {
				return completer.Run(ctx, client, defaultModel, prompt)
			}

			cli.ShowAppHelp(cmd)
			return nil
		},

		Commands: []*cli.Command{
			{
				Name:  "chat",
				Usage: "AI Chat",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return chat.Run(ctx, client, defaultModel)
				},
			},

			{
				Name:  "admin",
				Usage: "AI Admin",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return admin.Run(ctx, client, defaultModel, "")
				},
			},

			{
				Name:  "coder",
				Usage: "AI Coder",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					return coder.Run(ctx, client, defaultModel, "")
				},
			},

			{
				Name:  "openapi",
				Usage: "AI OpenAPI Client",

				HideHelp: true,

				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "file",
						Usage: "Specification",

						Required: true,
					},

					&cli.StringFlag{
						Name:  "url",
						Usage: "API Base URL",

						Required: true,
					},

					&cli.StringFlag{
						Name:  "bearer",
						Usage: "API Bearer",
					},

					&cli.StringFlag{
						Name:  "username",
						Usage: "API Username",
					},

					&cli.StringFlag{
						Name:  "password",
						Usage: "API Password",
					},
				},

				Action: func(ctx context.Context, cmd *cli.Command) error {
					path := cmd.String("file")

					url := cmd.String("url")
					bearer := cmd.String("bearer")
					username := cmd.String("username")
					password := cmd.String("password")

					return openapi.Run(ctx, client, defaultModel, path, url, bearer, username, password)
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
