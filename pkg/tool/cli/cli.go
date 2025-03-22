package cli

import (
	"context"
	"encoding/json"
	"os/exec"

	"github.com/adrianliechti/wingman/pkg/tool"
)

func New(name string) (*CLI, error) {
	_, err := exec.LookPath(name)

	if err != nil {
		return nil, err
	}

	fs := &CLI{
		name: name,
	}

	return fs, nil
}

var (
	_ tool.Provider = (*CLI)(nil)
)

type CLI struct {
	name string
}

func (c *CLI) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        "run_cli_" + c.name,
			Description: "run the `" + c.name + "` command line interface command with the given arguments",

			Schema: tool.Schema{
				"type": "object",

				"properties": map[string]any{
					"args": map[string]any{
						"type": "array",

						"items": map[string]any{
							"type": "string",
						},
					},
				},
			},

			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				data, err := json.Marshal(args)

				if err != nil {
					return nil, err
				}

				var parameters struct {
					Args []string `json:"args"`
				}

				if err := json.Unmarshal(data, &parameters); err != nil {
					return nil, err
				}

				output, err := exec.CommandContext(ctx, c.name, parameters.Args...).CombinedOutput()

				if err != nil {
					return nil, err
				}

				return string(output), nil
			},
		},
	}, nil
}
