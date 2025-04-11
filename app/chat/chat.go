package chat

import (
	"context"

	"github.com/adrianliechti/wingman-cli/pkg/cli"
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

	for {
		prompt, _ := cli.Prompt(">", "")

		if prompt == "" {
			continue
		}

		println()

		input.Messages = append(input.Messages, wingman.UserMessage(prompt))

		completion, err := client.Completions.New(ctx, input)

		if err != nil {
			return err
		}

		input.Messages = append(input.Messages, *completion.Message)

		println()
		println()
		println()
	}
}
