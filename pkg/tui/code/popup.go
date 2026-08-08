package code

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/sahilm/fuzzy"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

const popupMaxRows = 8

type PopupItem struct {
	ID             string
	Label          string
	Detail         string
	Group          string
	Keywords       string
	Shortcut       string
	Checked        bool
	Disabled       bool
	DisabledReason string
}

type popupKind int

const (
	popupCommands popupKind = iota
	popupFiles
	popupList
	popupPalette
)

// Popup is the single list component behind slash-command completion, file
// mentions and the model/effort pickers. It renders below the
// composer. Standalone pickers (popupList) capture all input; the other kinds
// filter as the user types in the editor.
type Popup struct {
	kind   popupKind
	title  string
	header []string

	items    []PopupItem
	filtered []int
	index    int
	offset   int

	multi       bool
	acceptEmpty bool
	labelOnly   bool
	selected    map[string]bool
	maxRows     int

	query    string
	accepted bool

	hotkeys  map[rune]string
	onAccept func(ids []string)
	onCancel func()
}

func newPopup(kind popupKind, title string, items []PopupItem, onAccept func(ids []string)) *Popup {
	p := &Popup{
		kind:     kind,
		title:    title,
		items:    items,
		selected: map[string]bool{},
		onAccept: onAccept,
		maxRows:  popupMaxRows,
	}
	p.SetQuery("")
	return p
}

type popupSource struct {
	items     []PopupItem
	labelOnly bool
}

func (s popupSource) String(i int) string {
	item := s.items[i]
	if s.labelOnly {
		return strings.Join([]string{item.Label, item.Keywords}, " ")
	}
	return strings.Join([]string{item.Label, item.Detail, item.Keywords, item.Group}, " ")
}
func (s popupSource) Len() int { return len(s.items) }

func (p *Popup) SetQuery(query string) {
	changed := query != p.query
	p.query = query
	p.filtered = p.filtered[:0]

	switch {
	case query == "":
		for i := range p.items {
			p.filtered = append(p.filtered, i)
		}
	case p.kind == popupCommands:
		for i, item := range p.items {
			if strings.HasPrefix(item.Label, query) {
				p.filtered = append(p.filtered, i)
			}
		}
	default:
		for _, m := range fuzzy.FindFrom(query, popupSource{items: p.items, labelOnly: p.labelOnly}) {
			p.filtered = append(p.filtered, m.Index)
		}
		if p.kind == popupPalette {
			// Fuzzy results arrive in score order; regrouping keeps each
			// group header rendering once while scores order items within.
			rank := make(map[string]int, len(p.items))
			for i, item := range p.items {
				if _, ok := rank[item.Group]; !ok {
					rank[item.Group] = i
				}
			}
			slices.SortStableFunc(p.filtered, func(left, right int) int {
				return cmp.Compare(rank[p.items[left].Group], rank[p.items[right].Group])
			})
		}
	}

	if changed || p.index < 0 || p.index >= len(p.filtered) {
		p.index = 0
		for i, idx := range p.filtered {
			if !p.items[idx].Disabled {
				p.index = i
				break
			}
		}
	}
	p.offset = 0
}

func (p *Popup) SetSelected(id string, on bool) {
	if on {
		p.selected[id] = true
	} else {
		delete(p.selected, id)
	}
}

func (p *Popup) SelectID(id string) {
	for i, idx := range p.filtered {
		if p.items[idx].ID == id {
			p.index = i
			return
		}
	}
}

func (p *Popup) Empty() bool {
	return len(p.filtered) == 0
}

func (p *Popup) Current() (PopupItem, bool) {
	if p.index < 0 || p.index >= len(p.filtered) {
		return PopupItem{}, false
	}
	return p.items[p.filtered[p.index]], true
}

func (p *Popup) accept() bool {
	var ids []string

	if p.multi {
		for _, idx := range p.filtered {
			if p.selected[p.items[idx].ID] {
				ids = append(ids, p.items[idx].ID)
			}
		}
		for id := range p.selected {
			found := slices.Contains(ids, id)
			if !found {
				ids = append(ids, id)
			}
		}
	} else if item, ok := p.Current(); ok {
		if item.Disabled {
			return false
		}
		ids = []string{item.ID}
	}

	if len(ids) > 0 || p.acceptEmpty {
		p.accepted = true
		if p.onAccept != nil {
			p.onAccept(ids)
		}
		return true
	}
	return false
}

func (p *Popup) acceptID(id string) {
	p.accepted = true
	if p.onAccept != nil {
		p.onAccept([]string{id})
	}
}

// HandleKey processes navigation. Returns (consumed, closed).
func (p *Popup) HandleKey(ev inline.KeyEvent) (bool, bool) {
	switch ev.Key {
	case inline.KeyUp:
		if p.index > 0 {
			p.index--
		}
		return true, false

	case inline.KeyDown:
		if p.index < len(p.filtered)-1 {
			p.index++
		}
		return true, false

	case inline.KeyPgUp:
		p.index -= p.maxRows
		if p.index < 0 {
			p.index = 0
		}
		return true, false

	case inline.KeyPgDn:
		if len(p.filtered) == 0 {
			p.index = 0
			return true, false
		}
		p.index += p.maxRows
		if p.index >= len(p.filtered) {
			p.index = len(p.filtered) - 1
		}
		return true, false

	case inline.KeyTab:
		if p.multi {
			if item, ok := p.Current(); ok {
				p.SetSelected(item.ID, !p.selected[item.ID])
				if p.index < len(p.filtered)-1 {
					p.index++
				}
			}
			return true, false
		}
		if p.kind == popupCommands {
			return true, p.accept()
		}
		return false, false

	case inline.KeyEnter:
		return true, p.accept()

	case inline.KeyEsc:
		return true, true
	}

	if p.kind == popupList || p.kind == popupPalette {
		switch ev.Key {
		case inline.KeyRune, inline.KeyBackspace:
			if ev.Key == inline.KeyBackspace {
				if p.query != "" {
					r := []rune(p.query)
					p.SetQuery(string(r[:len(r)-1]))
				}
			} else if !ev.Alt {
				if id, ok := p.hotkeys[ev.Rune]; ok {
					p.acceptID(id)
					return true, true
				}
				if p.multi && ev.Rune == ' ' {
					if item, ok := p.Current(); ok {
						p.SetSelected(item.ID, !p.selected[item.ID])
					}
					return true, false
				}
				p.SetQuery(p.query + string(ev.Rune))
			}
			return true, false
		case inline.KeyCtrl:
			if ev.Rune == 'c' {
				return true, true
			}
		}
		return true, false
	}

	return false, false
}

func (p *Popup) Render(width int) []string {
	t := theme.Default

	var lines []string

	for _, line := range p.header {
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}

	if p.title != "" {
		title := p.title
		if p.kind == popupList && p.query != "" {
			title += "  " + p.query
		}
		lines = append(lines, cellIndent+dim(title))
	}

	if len(p.filtered) == 0 {
		lines = append(lines, cellIndent+dim("  no matches"))
		return lines
	}

	visible := p.maxRows
	if visible <= 0 {
		visible = popupMaxRows
	}
	if p.index < p.offset {
		p.offset = p.index
	}
	if p.index >= p.offset+visible {
		p.offset = p.index - visible + 1
	}

	end := min(p.offset+visible, len(p.filtered))

	inner := max(width-len(cellIndent), 20)

	for i := p.offset; i < end; i++ {
		item := p.items[p.filtered[i]]
		if p.kind == popupPalette && item.Group != "" {
			previousGroup := ""
			if i > p.offset {
				previousGroup = p.items[p.filtered[i-1]].Group
			}
			if i == p.offset || item.Group != previousGroup {
				lines = append(lines, cellIndent+dim("  "+strings.ToUpper(item.Group)))
			}
		}

		marker := "  "
		if p.multi {
			marker = dim("□ ")
			if p.selected[item.ID] {
				marker = colored(t.Cyan, "■ ")
			}
		}

		check := ""
		if item.Checked {
			check = colored(t.Green, "✓ ")
		}

		var line string
		if i == p.index {
			line = colored(t.Cyan, "→ ") + marker + check + fg(t.Cyan) + item.Label + ansi.Reset
		} else {
			line = "  " + marker + check + item.Label
		}

		detail := item.Detail
		if item.Disabled && item.DisabledReason != "" {
			detail = item.DisabledReason
		}
		if detail != "" {
			line += "  " + dim(detail)
		}
		if item.Shortcut != "" {
			line += "  " + dim(item.Shortcut)
		}
		if item.Disabled {
			line = dim(ansi.Strip(line))
		}

		lines = append(lines, cellIndent+ansi.Truncate(line, inner, "…")+ansi.Reset)
	}

	if len(p.filtered) > visible {
		lines = append(lines, cellIndent+dim(fmt.Sprintf("  (%d/%d)", p.index+1, len(p.filtered))))
	}
	if p.kind == popupPalette {
		lines = append(lines, cellIndent+ansi.Truncate(dim("  ↑↓ navigate · enter open · esc back/close"), inner, "…"))
	}

	return lines
}
