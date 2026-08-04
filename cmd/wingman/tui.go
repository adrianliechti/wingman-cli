package main

import (
	"context"
	"os"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/code/agents"
	codetui "github.com/adrianliechti/wingman-agent/pkg/tui/code"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type tuiOptions struct {
	Agent     string
	SessionID string
}

func newTUIAgent(ctx context.Context, ws *code.Workspace, name string) (code.Agent, error) {
	return agents.New(ctx, ws, name, nil)
}

func latestSessionID(sessions []code.SessionInfo) string {
	id := ""
	var updated time.Time
	for _, s := range sessions {
		if id == "" || s.UpdatedAt.After(updated) {
			id = s.ID
			updated = s.UpdatedAt
		}
	}
	return id
}

func runTUI(ctx context.Context, opts tuiOptions) {
	theme.Auto()

	wd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	ws, err := code.NewWorkspace(wd)
	if err != nil {
		fatal(err)
	}

	defer ws.Close()

	wa, err := newTUIAgent(ctx, ws, opts.Agent)
	if err != nil {
		fatal(err)
	}

	sessionID := opts.SessionID
	if sessionID == "latest" {
		sessions, err := wa.ListSessions(ctx)
		if err != nil {
			_ = wa.Close()
			fatal(err)
		}
		sessionID = latestSessionID(sessions)
	}

	if sessionID != "" {
		if err := wa.LoadSession(ctx, sessionID); err != nil {
			_ = wa.Close()
			fatal(err)
		}
	} else {
		sessionID, err = wa.NewSession(ctx)
		if err != nil {
			_ = wa.Close()
			fatal(err)
		}
	}

	app := codetui.New(ctx, wa, sessionID)

	if err := app.Run(); err != nil {
		_ = wa.Close()
		fatal(err)
	}
}
