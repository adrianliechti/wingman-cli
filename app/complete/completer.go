package complete

import (
	"context"
	"strings"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func Run(ctx context.Context, client *wingman.Client, model, prompt string) error {
	prompt = strings.TrimSpace(prompt)

	if prompt == "" {
		return nil
	}

	input := wingman.CompletionRequest{
		Model: model,

		Messages: []wingman.Message{
			wingman.UserMessage(prompt),
		},

		CompleteOptions: wingman.CompleteOptions{
			Stream: func(ctx context.Context, completion wingman.Completion) error {
				print(completion.Message.Text())
				return nil
			},
		},
	}

	if _, err := client.Completions.New(ctx, input); err != nil {
		return err
	}

	cli.Info()

	return nil
}
