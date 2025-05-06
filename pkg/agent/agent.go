package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/markdown"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/util"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func Run(ctx context.Context, client *wingman.Client, model, prompt string, tools []tool.Tool) error {
	input := wingman.CompletionRequest{
		Model: model,

		CompleteOptions: wingman.CompleteOptions{
			Tools: util.ConvertTools(tools),
		},
	}

	if prompt != "" {
		input.Messages = append(input.Messages, wingman.SystemMessage(prompt))
	}

	for {
		prompt, err := cli.Text("", "")

		if err != nil {
			break
		}

		cli.Info()

		input.Messages = append(input.Messages, wingman.UserMessage(prompt))

		var message *wingman.Message

		for {
			var completion *wingman.Completion

			fn := func() error {
				completion, err = client.Completions.New(ctx, input)
				return err
			}

			if err := cli.Run("Thinking...", fn); err != nil {
				return err
			}

			message = completion.Message
			input.Messages = append(input.Messages, *message)

			calls := message.ToolCalls()

			if len(calls) == 0 {
				break
			}

			for _, call := range calls {
				content, err := handleToolCall(ctx, tools, call)

				if err != nil {
					content = err.Error()
				}

				input.Messages = append(input.Messages, wingman.ToolMessage(call.ID, content))
			}
		}

		if message == nil {
			return nil
		}

		markdown.Render(os.Stdout, message.Text())
	}

	return nil
}

func handleToolCall(ctx context.Context, tools []tool.Tool, call wingman.ToolCall) (string, error) {
	var handler tool.ExecuteFn

	for _, t := range tools {
		if !strings.EqualFold(t.Name, call.Name) {
			continue
		}

		handler = t.Execute
	}

	if handler == nil {
		return "", errors.New("Unknown tool: " + call.Name)
	}

	var args map[string]any
	json.Unmarshal([]byte(call.Arguments), &args)

	result, err := handler(ctx, args)

	if err != nil {
		return "", err
	}

	var content string

	switch v := result.(type) {
	case string:
		content = v
	default:
		data, _ := json.Marshal(v)
		content = string(data)
	}

	return content, nil
}
