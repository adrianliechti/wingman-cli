package chat

import (
	"context"

	"github.com/adrianliechti/wingman-cli/pkg/util"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func Run(ctx context.Context, client *wingman.Client, model string) error {
	input := wingman.CompletionRequest{
		Model: model,

		CompleteOptions: wingman.CompleteOptions{
			Stream: func(ctx context.Context, completion wingman.Completion) error {
				print(completion.Message.Text())
				return nil
			},
		},
	}

	if system, err := util.ParsePrompt(); err == nil {
		input.Messages = append(input.Messages, wingman.SystemMessage(system))
	}

	for {
		prompt, err := cli.Text("", "")

		if err != nil {
			break
		}

		if prompt == "" {
			continue
		}

		cli.Info()

		input.Messages = append(input.Messages, wingman.UserMessage(prompt))

		completion, err := client.Completions.New(ctx, input)

		if err != nil {
			return err
		}

		input.Messages = append(input.Messages, *completion.Message)

		cli.Info()
		cli.Info()
		cli.Info()
	}

	return nil
}
