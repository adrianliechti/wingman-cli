package server

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func newPromptTestServer() *Server {
	return &Server{
		pendingPrompts: map[string]pendingPrompt{},
		confirmAll:     map[string]bool{},
	}
}

func waitForPendingPrompt(t *testing.T, s *Server, sessionID string) string {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()

	for {
		s.promptsMu.Lock()
		for id, prompt := range s.pendingPrompts {
			if prompt.sid == sessionID {
				s.promptsMu.Unlock()
				return id
			}
		}
		s.promptsMu.Unlock()

		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("timed out waiting for pending prompt")
			return ""
		}
	}
}

func TestConfirmUsesActionAndSessionScope(t *testing.T) {
	s := newPromptTestServer()
	ctx, cancel := context.WithTimeout(code.WithSessionID(context.Background(), "session-1"), time.Second)
	defer cancel()

	type result struct {
		approved bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		approved, err := s.Confirm(ctx, "Run command?")
		done <- result{approved: approved, err: err}
	}()

	promptID := waitForPendingPrompt(t, s, "session-1")
	s.resolvePrompt(ClientMessage{
		PromptID: promptID,
		Action:   string(tool.ElicitAccept),
		Scope:    PromptScopeSession,
	})

	first := <-done
	if first.err != nil || !first.approved {
		t.Fatalf("Confirm() = (%v, %v), want (true, nil)", first.approved, first.err)
	}
	approved, err := s.Confirm(ctx, "Run another command?")
	if err != nil || !approved {
		t.Fatalf("remembered Confirm() = (%v, %v), want (true, nil)", approved, err)
	}
}

func TestElicitUsesActionAndStructuredContent(t *testing.T) {
	s := newPromptTestServer()
	ctx, cancel := context.WithTimeout(code.WithSessionID(context.Background(), "session-2"), time.Second)
	defer cancel()

	type result struct {
		value tool.ElicitResult
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := s.Elicit(ctx, tool.ElicitRequest{
			Message: "Choose a language",
			Fields:  []tool.ElicitField{{Name: "language"}},
		})
		done <- result{value: value, err: err}
	}()

	promptID := waitForPendingPrompt(t, s, "session-2")
	content := map[string]any{"language": "Go"}
	s.resolvePrompt(ClientMessage{
		PromptID: promptID,
		Action:   string(tool.ElicitAccept),
		Content:  content,
	})

	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	want := tool.ElicitResult{Action: tool.ElicitAccept, Content: content}
	if !reflect.DeepEqual(got.value, want) {
		t.Fatalf("Elicit() = %#v, want %#v", got.value, want)
	}
}
