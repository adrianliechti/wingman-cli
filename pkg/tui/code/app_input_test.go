package code

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func TestCtrlJInsertsNewline(t *testing.T) {
	a := &App{editor: NewEditor()}
	a.editor.SetText("first line")

	a.handleKey(inline.KeyEvent{Key: inline.KeyCtrl, Rune: 'j'})

	if got := a.editor.Text(); got != "first line\n" {
		t.Fatalf("editor text = %q, want newline without submission", got)
	}
}

func TestTabTogglesPlanAndAgent(t *testing.T) {
	agent := newUITestAgent(nil)
	a := &App{ctx: context.Background(), agent: agent, editor: NewEditor()}

	a.handleKey(inline.KeyEvent{Key: inline.KeyTab})
	if agent.mode != "plan" {
		t.Fatalf("Tab mode = %q, want plan", agent.mode)
	}
	a.handleKey(inline.KeyEvent{Key: inline.KeyTab})
	if agent.mode != "agent" {
		t.Fatalf("second Tab mode = %q, want agent", agent.mode)
	}
}

func TestBacktabTogglesUnattendedAndAgent(t *testing.T) {
	agent := newUITestAgent(nil)
	a := &App{ctx: context.Background(), agent: agent, editor: NewEditor()}

	a.handleKey(inline.KeyEvent{Key: inline.KeyBacktab})
	if agent.mode != "unattended" {
		t.Fatalf("Shift+Tab mode = %q, want unattended", agent.mode)
	}
	a.handleKey(inline.KeyEvent{Key: inline.KeyBacktab})
	if agent.mode != "agent" {
		t.Fatalf("second Shift+Tab mode = %q, want agent", agent.mode)
	}
}
