package code

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func TestPopupRecoversAfterEmptySearchAndCanAcceptEmptyMulti(t *testing.T) {
	p := newPopup(popupPalette, "commands", []PopupItem{{ID: "one", Label: "one"}}, nil)
	p.SetQuery("missing")
	p.HandleKey(inline.KeyEvent{Key: inline.KeyPgDn})
	p.SetQuery("")
	if item, ok := p.Current(); !ok || item.ID != "one" {
		t.Fatalf("popup did not recover current item: %+v, %v", item, ok)
	}

	accepted := false
	p = newPopup(popupList, "context", []PopupItem{{ID: "one", Label: "one"}}, func(ids []string) {
		accepted = len(ids) == 0
	})
	p.multi = true
	p.acceptEmpty = true
	_, closed := p.HandleKey(inline.KeyEvent{Key: inline.KeyEnter})
	if !closed || !accepted {
		t.Fatal("empty multi-selection was not applied")
	}
}

func TestPaletteFilterKeepsGroupsContiguousWithSingleHeaders(t *testing.T) {
	items := []PopupItem{
		{ID: "a", Label: "/diff", Detail: "Show changes from baseline", Group: "Workspace", Keywords: "diff"},
		{ID: "b", Label: "/problems", Detail: "Show problems", Group: "Workspace", Keywords: "problems"},
		{ID: "c", Label: "/resume", Detail: "Resume a previous session", Group: "Session", Keywords: "resume"},
		{ID: "d", Label: "/clear", Detail: "Clear chat history", Group: "Session", Keywords: "clear"},
		{ID: "e", Label: "/model", Detail: "Select AI model", Group: "Agent", Keywords: "model"},
		{ID: "f", Label: "/effort", Detail: "Set reasoning effort", Group: "Agent", Keywords: "effort"},
		{ID: "g", Label: "Open transcript", Detail: "Search and inspect the complete session", Group: "Workspace", Keywords: "history tools reasoning activity"},
		{ID: "h", Label: "/quit", Detail: "Exit application", Group: "Application", Keywords: "quit"},
	}

	p := newPopup(popupPalette, "commands", items, nil)
	p.maxRows = 20
	p.SetQuery("e")

	seen := map[string]bool{}
	last := ""
	for _, idx := range p.filtered {
		group := p.items[idx].Group
		if group != last && seen[group] {
			t.Fatalf("group %q split across the filtered list", group)
		}
		seen[group] = true
		last = group
	}

	headers := map[string]int{}
	for _, line := range p.Render(120) {
		switch text := strings.TrimSpace(ansi.Strip(line)); text {
		case "WORKSPACE", "SESSION", "AGENT", "APPLICATION":
			headers[text]++
		}
	}
	if len(headers) == 0 {
		t.Fatal("no group headers rendered")
	}
	for header, count := range headers {
		if count > 1 {
			t.Fatalf("header %s rendered %d times", header, count)
		}
	}
}

func TestQueryChangeResetsSelectionToFirstEnabled(t *testing.T) {
	p := newPopup(popupPalette, "commands", []PopupItem{
		{ID: "1", Label: "/model"},
		{ID: "2", Label: "/mode-x"},
		{ID: "3", Label: "/effort"},
		{ID: "4", Label: "/memory"},
	}, nil)
	p.HandleKey(inline.KeyEvent{Key: inline.KeyDown})
	p.HandleKey(inline.KeyEvent{Key: inline.KeyDown})

	p.SetQuery("m")
	if p.index != 0 {
		t.Fatalf("index = %d after query change, want 0", p.index)
	}

	p = newPopup(popupPalette, "commands", []PopupItem{
		{ID: "x", Label: "/plan", Disabled: true},
		{ID: "y", Label: "/plans"},
	}, nil)
	p.SetQuery("plan")
	if item, ok := p.Current(); !ok || item.Disabled {
		t.Fatalf("selection landed on a disabled item: %+v, %v", item, ok)
	}
}

func TestLabelOnlySearchIgnoresDetail(t *testing.T) {
	p := newPopup(popupList, "context", []PopupItem{
		{ID: "file:main.go", Label: "main.go", Detail: "workspace file"},
		{ID: "file:readme.md", Label: "readme.md", Detail: "workspace file"},
	}, nil)
	p.labelOnly = true

	p.SetQuery("file")
	if !p.Empty() {
		t.Fatalf("detail text matched: %v", p.filtered)
	}

	p.SetQuery("main")
	if item, ok := p.Current(); !ok || item.ID != "file:main.go" {
		t.Fatalf("label match = %+v, %v", item, ok)
	}
}
