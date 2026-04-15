package copilot

import (
	"context"
	"os"
	"os/exec"
)

func Run(ctx context.Context, args []string, options *RunOptions) error {
	if options == nil {
		options = new(RunOptions)
	}

	if options.Path == "" {
		options.Path = "copilot"
	}

	if options.Env == nil {
		options.Env = os.Environ()
	}

	cfg, err := NewConfig(ctx, options)

	if err != nil {
		return err
	}

	vars := map[string]string{
		"COPILOT_OFFLINE": "true",

		"COPILOT_PROVIDER_BASE_URL": cfg.BaseURL,
		"COPILOT_PROVIDER_API_KEY":  cfg.AuthToken,

		"COPILOT_MODEL": cfg.Model,
	}

	env := options.Env

	for k, v := range vars {
		env = append(env, k+"="+v)
	}

	cmd := exec.Command(options.Path, args...)
	cmd.Env = env

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
