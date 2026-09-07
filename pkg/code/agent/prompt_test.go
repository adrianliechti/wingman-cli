package agent

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

type promptCaptureUI struct {
	sessionID string
}

func (u *promptCaptureUI) Elicit(ctx context.Context, _ tool.ElicitRequest) (tool.ElicitResult, error) {
	u.sessionID = code.SessionIDFromContext(ctx)
	return tool.ElicitResult{Action: tool.ElicitAccept}, nil
}

func (u *promptCaptureUI) Confirm(ctx context.Context, _ string) (bool, error) {
	u.sessionID = code.SessionIDFromContext(ctx)
	return true, nil
}

func TestPromptContextUsesLastActiveSession(t *testing.T) {
	ui := &promptCaptureUI{}
	a := &Agent{ui: ui, sessions: map[string]*sessionState{"active": {}, "fallback": {}}}
	a.lastActive.Store("active")

	if _, err := a.elicit(context.Background(), tool.ElicitRequest{Message: "question"}); err != nil {
		t.Fatal(err)
	}
	if ui.sessionID != "active" {
		t.Fatalf("elicitation session = %q, want active", ui.sessionID)
	}

	if _, err := a.confirm(code.WithSessionID(context.Background(), "explicit"), "confirm"); err != nil {
		t.Fatal(err)
	}
	if ui.sessionID != "explicit" {
		t.Fatalf("confirmation session = %q, want explicit", ui.sessionID)
	}
}

func TestWebPromptContextNeverGuessesAnOwner(t *testing.T) {
	a := &Agent{options: Options{RequireSessionContext: true}, sessions: map[string]*sessionState{"one": {}, "two": {}}}
	a.lastActive.Store("one")
	if got := code.SessionIDFromContext(a.promptContext(context.Background())); got != "" {
		t.Fatalf("guessed session %q", got)
	}
	if got := code.SessionIDFromContext(a.promptContext(code.WithSessionID(context.Background(), "two"))); got != "two" {
		t.Fatalf("lost explicit owner %q", got)
	}
}
