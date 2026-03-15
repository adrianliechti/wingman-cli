package junie

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

func Run(ctx context.Context, args []string, options *RunOptions) error {
	if options == nil {
		options = new(RunOptions)
	}

	if options.Path == "" {
		options.Path = "junie"
	}

	if options.Env == nil {
		options.Env = os.Environ()
	}

	cfg, err := NewConfig(ctx, options)

	if err != nil {
		return err
	}

	modelName := "wingman"

	dir, err := os.MkdirTemp("", "junie-config-*")

	if err != nil {
		return err
	}

	defer os.RemoveAll(dir)

	configFile := filepath.Join(dir, modelName+".json")

	if err := os.WriteFile(configFile, []byte(cfg), 0644); err != nil {
		return err
	}

	arg := []string{
		"--model-default-locations", "false",
		"--model-location", dir,
		"--model", "custom:" + modelName,
	}

	args = append(arg, args...)

	cmd := exec.Command(options.Path, args...)
	cmd.Env = options.Env

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
