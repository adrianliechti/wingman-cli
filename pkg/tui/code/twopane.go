package code

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

const (
	paneListWidth     = 40
	twoPaneBreakpoint = 88
)

// twoPaneOverlay is the shared searchable frame behind /diff and /problems.
// Wide terminals show list and detail together; narrow terminals drill from
// the list into a full-width detail view.
type twoPaneOverlay struct {
	title  string
	status string

	items      func(selected bool, index int) string
	count      int
	content    func(index int) []string
	searchText func(index int) string

	filtered []int
	selected int

	focusRight bool
	detailView bool
	narrow     bool
	listOffset int

	detailOffset int
	detail       []string
	height       int

	query     string
	searching bool
}

func newTwoPaneOverlay(title, status string, count int, item func(selected bool, index int) string, content func(index int) []string, search func(index int) string) *twoPaneOverlay {
	o := &twoPaneOverlay{
		title: title, status: status, items: item, count: count,
		content: content, searchText: search,
	}
	o.applyFilter()
	return o
}

func (o *twoPaneOverlay) sourceIndex() int {
	if o.selected < 0 || o.selected >= len(o.filtered) {
		return -1
	}
	return o.filtered[o.selected]
}

func (o *twoPaneOverlay) applyFilter() {
	currentSource := o.sourceIndex()
	o.filtered = o.filtered[:0]
	query := strings.ToLower(strings.TrimSpace(o.query))
	for i := 0; i < o.count; i++ {
		text := ""
		if o.searchText != nil {
			text = o.searchText(i)
		}
		if query == "" || strings.Contains(strings.ToLower(text), query) {
			o.filtered = append(o.filtered, i)
		}
	}
	o.selected = 0
	if currentSource >= 0 {
		for i, source := range o.filtered {
			if source == currentSource {
				o.selected = i
				break
			}
		}
	}
	if len(o.filtered) == 0 {
		o.selected = -1
	}
	o.listOffset = 0
	o.loadDetail()
}

func (o *twoPaneOverlay) loadDetail() {
	o.detail = nil
	o.detailOffset = 0
	if index := o.sourceIndex(); index >= 0 && o.content != nil {
		o.detail = o.content(index)
	}
}

func (o *twoPaneOverlay) moveSelection(delta int) {
	next := o.selected + delta
	if next < 0 || next >= len(o.filtered) {
		return
	}
	o.selected = next
	o.loadDetail()
}

func (o *twoPaneOverlay) scrollDetail(delta int) {
	rows := max(1, o.height-3)
	o.detailOffset += delta
	o.detailOffset = min(o.detailOffset, max(0, len(o.detail)-rows))
	o.detailOffset = max(o.detailOffset, 0)
}

func (o *twoPaneOverlay) handleSearchKey(ev inline.KeyEvent) {
	switch ev.Key {
	case inline.KeyEnter:
		o.searching = false
	case inline.KeyEsc:
		o.query = ""
		o.searching = false
		o.applyFilter()
	case inline.KeyBackspace:
		if o.query != "" {
			_, size := utf8.DecodeLastRuneInString(o.query)
			o.query = o.query[:len(o.query)-size]
			o.applyFilter()
		}
	case inline.KeyRune:
		if !ev.Alt {
			o.query += string(ev.Rune)
			o.applyFilter()
		}
	}
}

func (o *twoPaneOverlay) HandlePaste(text string) bool {
	if !o.searching {
		return false
	}
	o.query = appendPastedSearchQuery(o.query, text)
	o.applyFilter()
	return true
}

func (o *twoPaneOverlay) HandleKey(ev inline.KeyEvent) bool {
	if o.searching {
		o.handleSearchKey(ev)
		return false
	}

	rows := max(1, o.height-3)
	switch ev.Key {
	case inline.KeyEsc:
		if o.query != "" {
			o.query = ""
			o.applyFilter()
			return false
		}
		if o.narrow && o.detailView {
			o.detailView = false
			return false
		}
		return true
	case inline.KeyTab:
		if o.narrow {
			o.detailView = !o.detailView && o.sourceIndex() >= 0
		} else {
			o.focusRight = !o.focusRight
		}
	case inline.KeyRight, inline.KeyEnter:
		if o.narrow && o.sourceIndex() >= 0 {
			o.detailView = true
		} else if !o.narrow {
			o.focusRight = true
		}
	case inline.KeyLeft:
		if o.narrow {
			o.detailView = false
		} else {
			o.focusRight = false
		}
	case inline.KeyUp:
		if o.detailView || o.focusRight {
			o.scrollDetail(-1)
		} else {
			o.moveSelection(-1)
		}
	case inline.KeyDown:
		if o.detailView || o.focusRight {
			o.scrollDetail(1)
		} else {
			o.moveSelection(1)
		}
	case inline.KeyPgUp:
		if o.detailView || o.focusRight {
			o.scrollDetail(-rows)
		} else {
			for range rows {
				o.moveSelection(-1)
			}
		}
	case inline.KeyPgDn:
		if o.detailView || o.focusRight {
			o.scrollDetail(rows)
		} else {
			for range rows {
				o.moveSelection(1)
			}
		}
	case inline.KeyCtrl:
		return ev.Rune == 'c'
	case inline.KeyRune:
		switch ev.Rune {
		case '/':
			o.searching = true
			o.query = ""
			o.applyFilter()
			if o.narrow {
				o.detailView = false
			}
		case 'q':
			return true
		case 'j':
			if o.detailView || o.focusRight {
				o.scrollDetail(1)
			} else {
				o.moveSelection(1)
			}
		case 'k':
			if o.detailView || o.focusRight {
				o.scrollDetail(-1)
			} else {
				o.moveSelection(-1)
			}
		case 'g':
			if o.detailView || o.focusRight {
				o.scrollDetail(-len(o.detail))
			} else if len(o.filtered) > 0 {
				o.selected = 0
				o.loadDetail()
			}
		case 'G':
			if o.detailView || o.focusRight {
				o.scrollDetail(len(o.detail))
			} else if len(o.filtered) > 0 {
				o.selected = len(o.filtered) - 1
				o.loadDetail()
			}
		}
	}
	return false
}

func (o *twoPaneOverlay) HandleMouse(ev inline.MouseEvent) {
	if ev.Kind != inline.MouseWheel {
		return
	}
	if o.detailView || o.focusRight {
		o.scrollDetail(ev.WheelDelta * 3)
		return
	}
	delta := ev.WheelDelta
	if delta < 0 {
		for range -delta * 2 {
			o.moveSelection(-1)
		}
	} else {
		for range delta * 2 {
			o.moveSelection(1)
		}
	}
}

func scrollMarker(row, rows, offset, total int) string {
	if total <= rows || rows <= 0 {
		return " "
	}
	thumb := max(1, rows*rows/total)
	start := 0
	if maxOffset := total - rows; maxOffset > 0 {
		start = offset * (rows - thumb) / maxOffset
	}
	if row >= start && row < start+thumb {
		return colored(theme.Default.Cyan, "█")
	}
	return dim("│")
}

func (o *twoPaneOverlay) renderHeader(width int, detail bool) []string {
	title := bold(o.title)
	if detail {
		if index := o.sourceIndex(); index >= 0 && o.searchText != nil {
			label, _, _ := strings.Cut(o.searchText(index), "\n")
			title += dim(" › ") + ansi.Truncate(label, max(10, width/2), "…")
		}
	} else {
		title += "  " + o.status
	}
	if o.searching || o.query != "" {
		title += colored(theme.Default.Cyan, "  /"+o.query)
		if o.query != "" {
			title += dim(" · " + strconv.Itoa(len(o.filtered)) + " matches")
		}
	}
	rule := colored(theme.Default.BrBlack, strings.Repeat("─", max(10, width)))
	return []string{ansi.Truncate(title, width, "…"), ansi.Truncate(rule, width, "…")}
}

func (o *twoPaneOverlay) Render(width, height int) []string {
	o.height = height
	o.narrow = width < twoPaneBreakpoint
	rows := max(0, height-3)

	if o.narrow {
		return o.renderNarrow(width, height, rows)
	}
	return o.renderWide(width, height, rows)
}

func (o *twoPaneOverlay) renderWide(width, height, rows int) []string {
	t := theme.Default
	listWidth := min(paneListWidth, max(28, width*36/100))
	detailWidth := max(10, width-listWidth-3)

	if o.selected < o.listOffset {
		o.listOffset = o.selected
	}
	if o.selected >= o.listOffset+rows {
		o.listOffset = o.selected - rows + 1
	}
	o.listOffset = max(0, o.listOffset)

	lines := o.renderHeader(width, false)
	for row := range rows {
		var left, right string
		position := o.listOffset + row
		if position >= 0 && position < len(o.filtered) {
			left = o.items(position == o.selected && !o.focusRight, o.filtered[position])
			left = ansi.Pad(ansi.Truncate(left, listWidth, "…"), listWidth)
			if position == o.selected && !o.focusRight {
				left = ansi.Highlight(left, 0, listWidth, ansi.Bg(t.Selection))
			}
		} else {
			left = strings.Repeat(" ", listWidth)
		}
		if index := o.detailOffset + row; index < len(o.detail) {
			right = o.detail[index]
		}
		divider := colored(t.BrBlack, "│")
		if o.focusRight {
			divider = colored(t.Cyan, "┃")
		}
		marker := scrollMarker(row, rows, o.detailOffset, len(o.detail))
		line := left + divider + " " + ansi.Pad(ansi.Truncate(right, detailWidth, "…"), detailWidth) + marker
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}
	hints := "↑↓/jk select · enter/right detail · tab switch · / filter · esc close"
	if o.focusRight {
		hints = "↑↓/jk scroll · g/G top/bottom · tab/left list · / filter · esc close"
	}
	if o.searching {
		hints = "type to filter · enter accept · esc clear"
	}
	lines = append(lines, ansi.Truncate(dim(hints), width, "…"))
	return lines[:min(len(lines), height)]
}

func (o *twoPaneOverlay) renderNarrow(width, height, rows int) []string {
	lines := o.renderHeader(width, o.detailView)
	if !o.detailView {
		if o.selected < o.listOffset {
			o.listOffset = o.selected
		}
		if o.selected >= o.listOffset+rows {
			o.listOffset = o.selected - rows + 1
		}
		o.listOffset = max(0, o.listOffset)
		for row := range rows {
			position := o.listOffset + row
			line := ""
			if position >= 0 && position < len(o.filtered) {
				line = o.items(position == o.selected, o.filtered[position])
				if position == o.selected {
					line = ansi.Highlight(ansi.Pad(ansi.Truncate(line, width, "…"), width), 0, width, ansi.Bg(theme.Default.Selection))
				}
			}
			lines = append(lines, ansi.Truncate(line, width, "…"))
		}
		hints := "↑↓/jk select · enter/right open · / filter · esc close"
		if o.searching {
			hints = "type to filter · enter accept · esc clear"
		}
		lines = append(lines, ansi.Truncate(dim(hints), width, "…"))
		return lines[:min(len(lines), height)]
	}

	for row := range rows {
		line := ""
		if index := o.detailOffset + row; index < len(o.detail) {
			line = o.detail[index]
		}
		marker := scrollMarker(row, rows, o.detailOffset, len(o.detail))
		lines = append(lines, ansi.Pad(ansi.Truncate(line, max(1, width-1), "…"), max(1, width-1))+marker)
	}
	lines = append(lines, ansi.Truncate(dim("↑↓/jk scroll · g/G top/bottom · left/esc back"), width, "…"))
	return lines[:min(len(lines), height)]
}
