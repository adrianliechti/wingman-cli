package opencode

import (
	"context"
	"os"
	"os/exec"

	"github.com/adrianliechti/wingman-cli/pkg/cli"
)

func Run(ctx context.Context, args []string, options *cli.RunOptions) error {
	if options == nil {
		options = new(cli.RunOptions)
	}

	if options.Path == "" {
		options.Path = "opencode"
	}

	if options.Env == nil {
		options.Env = os.Environ()
	}

	cfg, err := NewConfig(ctx, options)

	if err != nil {
		return err
	}

	env := options.Env
	env = append(env, "OPENCODE_CONFIG_CONTENT="+cfg)

	cmd := exec.Command(options.Path, args...)
	cmd.Env = env

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
