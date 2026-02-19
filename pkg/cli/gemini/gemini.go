package gemini

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
		options.Path = "gemini"
	}

	if options.Env == nil {
		options.Env = os.Environ()
	}

	cfg, err := NewConfig(ctx, options)

	if err != nil {
		return err
	}

	vars := map[string]string{
		"GOOGLE_GEMINI_BASE_URL": cfg.BaseURL,

		"GEMINI_DEFAULT_AUTH_TYPE":      "gemini-api-key",
		"GEMINI_API_KEY":                cfg.AuthToken,
		"GEMINI_API_KEY_AUTH_MECHANISM": "bearer",

		"GEMINI_MODEL": cfg.Model,

		"GEMINI_TELEMETRY_ENABLED": "false",
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
