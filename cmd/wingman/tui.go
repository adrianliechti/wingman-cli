package main

import (
	"context"
	"fmt"
	"os"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	acpclaude "github.com/adrianliechti/wingman-agent/pkg/acp/claude"
	acpcodex "github.com/adrianliechti/wingman-agent/pkg/acp/codex"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeacp "github.com/adrianliechti/wingman-agent/pkg/code/acp"
	coder "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	codetui "github.com/adrianliechti/wingman-agent/pkg/tui/code"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type tuiOptions struct {
	Agent     string
	SessionID string
}

func newTUIAgent(ctx context.Context, ws *code.Workspace, name string) (code.Agent, error) {
	switch name {
	case "", code.BuiltinAgentName:
		cfg, err := agent.DefaultConfig()
		if err != nil {
			return nil, err
		}
		return coder.New(ws, cfg, nil), nil

	case "codex":
		srv, err := acpcodex.Spawn(ctx, acpcodex.Options{
			Dir: ws.RootPath,
			Env: os.Environ(),
		})
		if err != nil {
			return nil, err
		}
		a, err := codeacp.NewInProcess(ws, name, srv, func(conn *acpsdk.AgentSideConnection) {
			srv.SetAgentConnection(conn)
		}, srv.Close)
		if err != nil {
			_ = srv.Close()
			return nil, err
		}
		return a, nil

	case "claude":
		srv := acpclaude.New(acpclaude.Options{
			Cwd: ws.RootPath,
			Env: os.Environ(),
		})
		a, err := codeacp.NewInProcess(ws, name, srv, func(conn *acpsdk.AgentSideConnection) {
			srv.SetAgentConnection(conn)
		}, srv.Close)
		if err != nil {
			_ = srv.Close()
			return nil, err
		}
		return a, nil

	default:
		return nil, fmt.Errorf("unknown agent %q (choose wingman, codex, or claude)", name)
	}
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
