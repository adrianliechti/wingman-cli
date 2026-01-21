package main

import (
	"context"
	"fmt"
	"os"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/app"
	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func main() {
	theme.Auto()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, cleanup, err := config.Default()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	defer cleanup()

	agent := agent.New(cfg)

	app := app.New(ctx, cfg, agent)

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
