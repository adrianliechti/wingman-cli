package cmd

import (
	"context"
	"os/exec"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func New(name string) (*Command, error) {
	_, err := exec.LookPath(name)

	if err != nil {
		return nil, err
	}

	c := &Command{
		name: name,
	}

	return c, nil
}

var (
	_ tool.Provider = (*Command)(nil)
)

type Command struct {
	name string
}

func (c *Command) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        "run_cli_" + c.name,
			Description: "run the `" + c.name + "` command line interface command with the given arguments",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"args": {
						Type: "array",

						Items: &tool.Schema{
							Type: "string",
						},
					},
				},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				argsInterface := params["args"]
				var args []string

				if argsInterface != nil {
					if argsSlice, ok := argsInterface.([]interface{}); ok {
						for _, arg := range argsSlice {
							if argStr, ok := arg.(string); ok {
								args = append(args, argStr)
							}
						}
					}
				}

				output, err := exec.CommandContext(ctx, c.name, args...).CombinedOutput()

				if err != nil {
					return nil, err
				}

				return string(output), nil
			},
		},
	}, nil
}
