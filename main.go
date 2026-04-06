package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/app"
	"github.com/adrianliechti/wingman-agent/pkg/proxy"

	"github.com/adrianliechti/wingman-agent/pkg/cli/claude"
	"github.com/adrianliechti/wingman-agent/pkg/cli/codex"
	"github.com/adrianliechti/wingman-agent/pkg/cli/gemini"
	"github.com/adrianliechti/wingman-agent/pkg/cli/junie"
	"github.com/adrianliechti/wingman-agent/pkg/cli/opencode"

	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

func saveExecutablePath() {
	path := os.Getenv("WINGMAN_PATH")

	if path == "" {
		exe, err := os.Executable()

		if err != nil {
			return
		}

		path = exe
	}

	if path == "" {
		return
	}

	home, err := os.UserHomeDir()

	if err != nil {
		return
	}

	dir := filepath.Join(home, ".wingman")
	os.MkdirAll(dir, 0755)

	os.WriteFile(filepath.Join(dir, "path"), []byte(path), 0644)
}

func main() {
	saveExecutablePath()

	ctx := context.Background()

	if len(os.Args) > 1 && os.Getenv("WINGMAN_URL") != "" {
		if os.Args[1] == "proxy" {
			fs := flag.NewFlagSet("proxy", flag.ExitOnError)
			port := fs.Int("port", 4242, "port to listen on")
			fs.Parse(os.Args[2:])

			if err := proxy.Run(ctx, proxy.ProxyOptions{Port: *port}); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		if os.Args[1] == "codex" {
			codex.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "claude" {
			claude.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "gemini" {
			gemini.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "junie" {
			junie.Run(ctx, os.Args[2:], nil)
			return
		}

		if os.Args[1] == "opencode" {
			opencode.Run(ctx, os.Args[2:], nil)
			return
		}
	}

	var resumeID string

	if len(os.Args) > 1 && os.Args[1] == "--resume" {
		if len(os.Args) > 2 {
			resumeID = os.Args[2] // wingman --resume <session-id>
		} else {
			resumeID = "latest"
		}
	}

	theme.Auto()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, cleanup, err := agent.DefaultConfig()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	defer cleanup()

	a := agent.New(cfg)

	if resumeID != "" {
		if resumeID == "latest" {
			sessions, _ := a.ListSessions()

			if len(sessions) > 0 {
				resumeID = sessions[0].ID
				a.LoadSession(resumeID)
			}
		} else {
			if err := a.LoadSession(resumeID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to resume session %s: %v\n", resumeID, err)
				os.Exit(1)
			}
		}
	}

	application := app.New(ctx, a, resumeID)

	if err := application.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
