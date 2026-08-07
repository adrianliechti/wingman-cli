package code

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func TestCtrlPOpensGroupedCommandCenterAndCtrlKStillEdits(t *testing.T) {
	a := &App{ctx: context.Background(), agent: newUITestAgent(nil), editor: NewEditor()}
	a.editor.SetText("keep remove")
	a.editor.cursor = 4
	a.handleKey(inline.KeyEvent{Key: inline.KeyCtrl, Rune: 'k'})
	if got := a.editor.Text(); got != "keep" {
		t.Fatalf("ctrl+k text = %q", got)
	}

	a.handleKey(inline.KeyEvent{Key: inline.KeyCtrl, Rune: 'p'})
	if a.popup == nil || a.popup.kind != popupPalette {
		t.Fatal("ctrl+p did not open command center")
	}
	groups := map[string]bool{}
	for _, item := range a.popup.items {
		groups[item.Group] = true
	}
	for _, want := range []string{"Workspace", "Session", "Agent", "Application"} {
		if !groups[want] {
			t.Errorf("missing %s group", want)
		}
	}
	if groups["Appearance"] {
		t.Error("removed theme feature left an Appearance group")
	}
}

func TestCommandCenterRefreshesWhenBusyStateFlips(t *testing.T) {
	a := &App{ctx: context.Background(), agent: newUITestAgent(nil), editor: NewEditor()}
	a.phase.Store(int32(PhaseThinking))

	a.showCommandCenter()
	if item := findPopupItem(a.popup, "builtin:/model"); item == nil || !item.Disabled {
		t.Fatal("/model not disabled while busy")
	}
	if findPopupItem(a.popup, "action:interrupt") == nil {
		t.Fatal("interrupt action missing while busy")
	}

	a.popup.SetQuery("model")
	a.popup.SelectID("builtin:/model")

	a.phase.Store(int32(PhaseIdle))
	a.refreshCommandCenter()

	if a.popup == nil || a.popup.query != "model" {
		t.Fatal("query not preserved across refresh")
	}
	if item := findPopupItem(a.popup, "builtin:/model"); item == nil || item.Disabled {
		t.Fatal("/model still disabled after the turn finished")
	}
	if findPopupItem(a.popup, "action:interrupt") != nil {
		t.Fatal("interrupt action still listed while idle")
	}
	if item, ok := a.popup.Current(); !ok || item.ID != "builtin:/model" {
		t.Fatalf("selection not preserved: %+v, %v", item, ok)
	}
}

func TestModelCommandSelectsModelThenEffort(t *testing.T) {
	agent := newUITestAgent(nil)
	a := &App{ctx: context.Background(), agent: agent, editor: NewEditor()}

	a.showModelPicker()
	if item, ok := a.popup.Current(); !ok || item.ID != agent.model || !item.Checked {
		t.Fatalf("initial model selection = %+v, %v", item, ok)
	}

	a.handlePopupKey(inline.KeyEvent{Key: inline.KeyEnter})
	if a.popup == nil || a.popup.title != "effort" {
		t.Fatalf("model selection did not open effort step: %+v", a.popup)
	}
	if item, ok := a.popup.Current(); !ok || item.ID != "medium" || !item.Checked {
		t.Fatalf("initial effort selection = %+v, %v", item, ok)
	}

	a.popup.SelectID("high")
	a.handlePopupKey(inline.KeyEvent{Key: inline.KeyEnter})
	if a.popup != nil {
		t.Fatal("effort selection did not close the picker")
	}
	if agent.effort != "high" {
		t.Fatalf("effort = %q, want high", agent.effort)
	}
	if a.toast != nil {
		t.Fatalf("model flow showed an unnecessary toast: %+v", a.toast)
	}
}

func TestModelCommandUsesDefaultEffortFallback(t *testing.T) {
	agent := newUITestAgent(nil)
	agent.effort = ""
	agent.efforts = []string{"low", "default", "high"}
	a := &App{ctx: context.Background(), agent: agent, editor: NewEditor()}

	a.showModelPicker()
	a.handlePopupKey(inline.KeyEvent{Key: inline.KeyEnter})

	if item, ok := a.popup.Current(); !ok || item.ID != "default" || !item.Checked {
		t.Fatalf("fallback effort selection = %+v, %v", item, ok)
	}
}

func TestEffortIsNotAStandaloneBuiltinCommand(t *testing.T) {
	a := &App{ctx: context.Background(), agent: newUITestAgent(nil), editor: NewEditor()}
	if command := a.findBuiltin("/effort"); command != nil {
		t.Fatalf("found removed command: %+v", command)
	}
	a.showCommandCenter()
	if item := findPopupItem(a.popup, "builtin:/effort"); item != nil {
		t.Fatalf("command center contains removed command: %+v", item)
	}
}

func TestCommandCenterShowsAgentModes(t *testing.T) {
	a := &App{ctx: context.Background(), agent: newUITestAgent(nil), editor: NewEditor()}
	a.showCommandCenter()
	if item := findPopupItem(a.popup, "builtin:/unattended"); item == nil || item.Group != "Agent" || item.Shortcut != "shift+tab" || item.Checked {
		t.Fatalf("unattended mode command = %+v", item)
	}
	if agentItem := findPopupItem(a.popup, "builtin:/agent"); agentItem == nil || agentItem.Shortcut != "tab" || !agentItem.Checked {
		t.Fatalf("current agent mode command = %+v", agentItem)
	}
}

func findPopupItem(p *Popup, id string) *PopupItem {
	for i := range p.items {
		if p.items[i].ID == id {
			return &p.items[i]
		}
	}
	return nil
}
