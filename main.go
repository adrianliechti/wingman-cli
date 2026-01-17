package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/app"
	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func main() {
	theme.Auto()

	cfg, err := config.Default()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	setupCleanup(cfg)

	ctx := context.Background()
	agent := agent.New(cfg)

	a := app.New(ctx, cfg, agent)

	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cfg.Cleanup()
		os.Exit(1)
	}

	cfg.Cleanup()
}

func setupCleanup(cfg *config.Config) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		cfg.Cleanup()
		os.Exit(0)
	}()
}
