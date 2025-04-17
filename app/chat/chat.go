package chat

import (
	"context"
	"errors"
	"os"

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

	if system, err := parsePrompt(); err == nil {
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

	return nil
}

func parsePrompt() (string, error) {
	for _, name := range []string{".prompt.md", ".prompt.txt", "prompt.md", "prompt.txt"} {
		data, err := os.ReadFile(name)

		if err != nil {
			continue
		}

		return string(data), nil
	}

	return "", errors.New("prompt file not found")
}
