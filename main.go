package main

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-cli/app/agent"
	"github.com/adrianliechti/wingman-cli/app/chat"
	"github.com/adrianliechti/wingman-cli/app/complete"
	"github.com/adrianliechti/wingman-cli/app/rag"
	"github.com/adrianliechti/wingman-cli/pkg/ingest"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"

	"github.com/joho/godotenv"
)

var version string

func main() {
	godotenv.Load()

	ctx := context.Background()

	app := initApp()

	if err := app.Run(ctx, os.Args); err != nil {
		panic(err)
	}
}

func initApp() cli.Command {
	url := os.Getenv("WINGMAN_URL")
	model := os.Getenv("WINGMAN_MODEL")
	token := os.Getenv("WINGMAN_TOKEN")

	if url == "" {
		url = "http://localhost:8080"
	}

	var options []wingman.RequestOption

	if token != "" {
		options = append(options, wingman.WithToken(token))
	}

	client := wingman.New(url, options...)

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

				cli.Info()
				return complete.Run(ctx, client, model, prompt)
			}

			if cmd.Args().Len() > 0 {
				cli.Info()
				return complete.Run(ctx, client, model, prompt)
			}

			return cli.ShowCommandHelp(cmd)
		},

		Commands: []*cli.Command{
			{
				Name:  "chat",
				Usage: "AI Chat",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					cli.Info()
					return chat.Run(ctx, client, model)
				},
			},

			{
				Name:  "rag",
				Usage: "Local RAG Chat",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					cli.Info()
					return rag.Run(ctx, client, model)
				},
			},

			{
				Name:  "ingest",
				Usage: "Server Side RAG Ingest",

				HideHelp: true,

				Flags: []cli.Flag{

					&cli.StringFlag{
						Name:  "index",
						Usage: "Name of RAG index, e.g. 'docs'",

						Required: true,
					},

					&cli.StringFlag{
						Name:  "dir",
						Usage: "Directory to index",

						Required: true,
					},

					&cli.StringFlag{
						Name:  "embedding",
						Usage: "Embedding, e.g. 'text-embedding-3-large'",

						Required: true,
					},
				},

				Action: func(ctx context.Context, cmd *cli.Command) error {
					index := cmd.String("index")
					dir := cmd.String("dir")
					embedding := cmd.String("embedding")

					cli.Info()
					return ingest.RunIngest(ctx, client, model, url, token, index, dir, embedding)
				},
			},

			{
				Name:  "mcp",
				Usage: "MCP Agent",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					cli.Info()
					return agent.RunMCP(ctx, client, model)
				},
			},

			{
				Name:  "coder",
				Usage: "Code Agent",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					cli.Info()
					return agent.RunCoder(ctx, client, model)
				},
			},

			{
				Name:  "azure",
				Usage: "Azure Agent",

				HideHelp: true,

				Action: func(ctx context.Context, cmd *cli.Command) error {
					cli.Info()
					return agent.RunAzure(ctx, client, model)
				},
			},

			{
				Name:  "openapi",
				Usage: "OpenAPI Agent",

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

					cli.Info()
					return agent.RunOpenAPI(ctx, client, model, path, url, bearer, username, password)
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
