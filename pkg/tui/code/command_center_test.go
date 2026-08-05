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

func findPopupItem(p *Popup, id string) *PopupItem {
	for i := range p.items {
		if p.items[i].ID == id {
			return &p.items[i]
		}
	}
	return nil
}
