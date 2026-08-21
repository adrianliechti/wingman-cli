package code

import (
	"context"
	"testing"

	corecode "github.com/adrianliechti/wingman-agent/pkg/code"
)

func TestConfirmAllIsScopedToConversationSession(t *testing.T) {
	a := &App{sessionID: "fallback"}
	ctxA := corecode.WithSessionID(context.Background(), "session-a")
	ctxB := corecode.WithSessionID(context.Background(), "session-b")

	a.rememberConfirmAll(ctxA)
	if !a.confirmAllForSession(ctxA) {
		t.Fatal("remembered approval missing from its session")
	}
	if a.confirmAllForSession(ctxB) {
		t.Fatal("remembered approval crossed into another session")
	}
	if a.confirmAllForSession(context.Background()) {
		t.Fatal("remembered approval crossed into the active fallback session")
	}
}
