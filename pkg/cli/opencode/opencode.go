package opencode

import (
	"context"
	"os"
	"os/exec"
)

func Run(ctx context.Context, args []string) error {
	cfg, err := NewConfig(ctx)

	if err != nil {
		return err
	}

	env := os.Environ()
	env = append(env, "OPENCODE_CONFIG_CONTENT="+cfg)

	cmd := exec.Command("opencode", args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
