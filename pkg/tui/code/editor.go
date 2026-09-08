package code

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

// Editor is a multiline input bounded by horizontal rules. The rule color is
// a status channel (mode, activity) set by the app.
type Editor struct {
	value  []rune
	cursor int

	placeholder string
	ruleColor   ansi.Color

	history []string
	histIdx int
	draft   string

	scroll         int
	rowWidth       int
	wrappedRows    []editorRow
	verticalColumn int
}

type EditorChrome struct {
	TopLabel    string
	BottomLeft  string
	BottomRight string
	TopColor    ansi.Color
	Attachments []string
}

const editorInset = 2

func NewEditor() *Editor {
	return &Editor{
		placeholder:    "Ask anything...",
		ruleColor:      theme.Default.BrBlack,
		histIdx:        -1,
		verticalColumn: -1,
	}
}

func (e *Editor) Text() string {
	return string(e.value)
}

func (e *Editor) SetText(text string) {
	e.ResetHistoryCursor()
	e.setText(text)
}

func (e *Editor) setText(text string) {
	e.value = []rune(text)
	e.cursor = len(e.value)
	e.wrappedRows = nil
	e.verticalColumn = -1
}

func (e *Editor) SetPlaceholder(p string) {
	e.placeholder = p
}

func (e *Editor) SetRuleColor(c ansi.Color) {
	e.ruleColor = c
}

func (e *Editor) AddHistory(entry string) {
	if entry == "" {
		return
	}
	if n := len(e.history); n > 0 && e.history[n-1] == entry {
		e.histIdx = -1
		return
	}
	e.history = append(e.history, entry)
	e.histIdx = -1
}

func (e *Editor) Insert(text string) {
	if text == "" {
		return
	}
	e.ResetHistoryCursor()
	e.wrappedRows = nil
	runes := []rune(text)
	e.value = append(e.value[:e.cursor], append(runes, e.value[e.cursor:]...)...)
	e.cursor += len(runes)
}

// ReplaceRange substitutes the rune range [from, to) with text and leaves the
// cursor after the inserted text.
func (e *Editor) ReplaceRange(from, to int, text string) {
	if from < 0 {
		from = 0
	}
	if to > len(e.value) {
		to = len(e.value)
	}
	if from > to {
		return
	}
	e.ResetHistoryCursor()
	e.wrappedRows = nil
	runes := []rune(text)
	e.value = append(e.value[:from], append(runes, e.value[to:]...)...)
	e.cursor = from + len(runes)
}

func (e *Editor) lineBounds() (start, end int) {
	start = e.cursor
	for start > 0 && e.value[start-1] != '\n' {
		start--
	}
	end = e.cursor
	for end < len(e.value) && e.value[end] != '\n' {
		end++
	}
	return
}

func (e *Editor) deleteRange(from, to int) {
	if from < 0 {
		from = 0
	}
	if to > len(e.value) {
		to = len(e.value)
	}
	if from >= to {
		return
	}
	e.ResetHistoryCursor()
	e.wrappedRows = nil
	e.value = append(e.value[:from], e.value[to:]...)
	if e.cursor > to {
		e.cursor -= to - from
	} else if e.cursor > from {
		e.cursor = from
	}
}

func isWordRune(r rune) bool {
	return r != ' ' && r != '\t' && r != '\n' && r != '/' && r != '.'
}

func (e *Editor) prevWord() int {
	i := e.cursor
	for i > 0 && !isWordRune(e.value[i-1]) {
		i--
	}
	for i > 0 && isWordRune(e.value[i-1]) {
		i--
	}
	return i
}

func (e *Editor) nextWord() int {
	i := e.cursor
	for i < len(e.value) && !isWordRune(e.value[i]) {
		i++
	}
	for i < len(e.value) && isWordRune(e.value[i]) {
		i++
	}
	return i
}

// HandleKey processes an input event. It reports whether the event was
// consumed; unconsumed navigation (history recall) is handled by the caller.
func (e *Editor) HandleKey(ev inline.KeyEvent) bool {
	if ev.Key != inline.KeyUp && ev.Key != inline.KeyDown {
		e.verticalColumn = -1
	}
	switch ev.Key {
	case inline.KeyRune:
		if ev.Alt {
			switch ev.Rune {
			case 'b':
				e.cursor = e.prevWord()
				return true
			case 'f':
				e.cursor = e.nextWord()
				return true
			}
			return false
		}
		e.Insert(string(ev.Rune))
		return true

	case inline.KeyBackspace:
		if ev.Alt {
			e.deleteRange(e.prevWord(), e.cursor)
			return true
		}
		if e.cursor > 0 {
			e.deleteRange(e.cursor-1, e.cursor)
		}
		return true

	case inline.KeyDelete:
		if e.cursor < len(e.value) {
			e.deleteRange(e.cursor, e.cursor+1)
		}
		return true

	case inline.KeyLeft:
		if ev.Alt {
			e.cursor = e.prevWord()
			return true
		}
		if e.cursor > 0 {
			e.cursor--
		}
		return true

	case inline.KeyRight:
		if ev.Alt {
			e.cursor = e.nextWord()
			return true
		}
		if e.cursor < len(e.value) {
			e.cursor++
		}
		return true

	case inline.KeyUp:
		return e.moveVertical(-1)

	case inline.KeyDown:
		return e.moveVertical(1)

	case inline.KeyHome:
		start, _ := e.lineBounds()
		e.cursor = start
		return true

	case inline.KeyEnd:
		_, end := e.lineBounds()
		e.cursor = end
		return true

	case inline.KeyCtrl:
		switch ev.Rune {
		case 'a':
			start, _ := e.lineBounds()
			e.cursor = start
			return true
		case 'e':
			_, end := e.lineBounds()
			e.cursor = end
			return true
		case 'b':
			if e.cursor > 0 {
				e.cursor--
			}
			return true
		case 'f':
			if e.cursor < len(e.value) {
				e.cursor++
			}
			return true
		case 'u':
			start, _ := e.lineBounds()
			e.deleteRange(start, e.cursor)
			return true
		case 'k':
			_, end := e.lineBounds()
			e.deleteRange(e.cursor, end)
			return true
		case 'w':
			e.deleteRange(e.prevWord(), e.cursor)
			return true
		case 'j':
			e.Insert("\n")
			return true
		case 'd':
			if e.cursor < len(e.value) {
				e.deleteRange(e.cursor, e.cursor+1)
			}
			return true
		}
		return false
	}

	return false
}

// moveVertical follows the displayed rows, including soft wraps. Only the
// first and last displayed rows hand up/down back to history navigation.
func (e *Editor) moveVertical(delta int) bool {
	width := e.rowWidth
	if width == 0 {
		width = max(len(e.value)*2, 1)
	}
	rows := e.rows(width)
	current, col := e.cursorPosition(rows)
	if e.verticalColumn < 0 {
		e.verticalColumn = col
	}
	col = e.verticalColumn
	target := current + delta
	if target < 0 || target >= len(rows) {
		return false
	}
	row := rows[target]
	e.cursor = row.start
	used := 0
	for _, r := range row.text {
		used += runeWidth(r)
		if used > col {
			break
		}
		e.cursor++
	}
	// A wrap boundary belongs to the following row; keep upward movement
	// inside its target row even when that row is shorter.
	if target+1 < len(rows) && e.cursor == rows[target+1].start && e.cursor > row.start {
		e.cursor--
	}
	return true
}

func (e *Editor) HistoryPrev() bool {
	if len(e.history) == 0 {
		return false
	}
	if e.histIdx == -1 {
		e.draft = e.Text()
		e.histIdx = len(e.history)
	}
	if e.histIdx == 0 {
		return true
	}
	e.histIdx--
	e.setText(e.history[e.histIdx])
	return true
}

func (e *Editor) HistoryNext() bool {
	if e.histIdx == -1 {
		return false
	}
	e.histIdx++
	if e.histIdx >= len(e.history) {
		e.histIdx = -1
		e.setText(e.draft)
		return true
	}
	e.setText(e.history[e.histIdx])
	return true
}

func (e *Editor) ResetHistoryCursor() {
	e.histIdx = -1
	e.draft = ""
	e.verticalColumn = -1
}

type editorRow struct {
	text      string
	start     int
	runeCount int
}

// rows soft-wraps the buffer for display; start indexes let cursor position
// map to a row/col.
func (e *Editor) rows(inner int) []editorRow {
	if inner < 1 {
		inner = 1
	}
	if e.rowWidth == inner && e.wrappedRows != nil {
		return e.wrappedRows
	}
	if e.rowWidth != inner {
		e.verticalColumn = -1
	}

	var rows []editorRow
	lineStart := 0
	text := e.value

	flushLine := func(start, end int) {
		if start == end {
			rows = append(rows, editorRow{text: "", start: start})
			return
		}
		segStart := start
		width := 0
		lastSpace := -1
		for i := start; i < end; i++ {
			w := runeWidth(text[i])
			if text[i] == ' ' {
				lastSpace = i
			}
			if width+w > inner && i > segStart {
				breakAt := i
				if lastSpace > segStart {
					breakAt = lastSpace + 1
				}
				rows = append(rows, editorRow{text: string(text[segStart:breakAt]), start: segStart, runeCount: breakAt - segStart})
				segStart = breakAt
				lastSpace = -1
				width = 0
				for j := segStart; j <= i; j++ {
					width += runeWidth(text[j])
				}
				continue
			}
			width += w
		}
		rows = append(rows, editorRow{text: string(text[segStart:end]), start: segStart, runeCount: end - segStart})
	}

	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == '\n' {
			flushLine(lineStart, i)
			lineStart = i + 1
		}
	}

	e.rowWidth = inner
	e.wrappedRows = rows
	return rows
}

func (e *Editor) cursorPosition(rows []editorRow) (rowIndex, col int) {
	for i, row := range rows {
		if e.cursor >= row.start && e.cursor <= row.start+row.runeCount {
			rowIndex = i
			col = ansi.Width(string(e.value[row.start:e.cursor]))
		}
	}
	return
}

func runeWidth(r rune) int {
	return ansi.Width(string(r))
}

// Render returns the editor lines (rules included) and the cursor position
// relative to the returned block.
func (e *Editor) Render(width, maxRows int, chrome EditorChrome) ([]string, inline.Pos) {
	t := theme.Default
	inner := width - 2*len(cellIndent) - 2*editorInset
	continuationPrefix := cellIndent + strings.Repeat(" ", editorInset)
	promptPrefix := cellIndent + colored(chrome.TopColor, "❯ ")
	e.ruleColor = chrome.TopColor

	rule := func(left, right string, leftDashes int) string {
		ruleWidth := max(width-2*len(cellIndent), 1)
		if left == "" && right == "" {
			return cellIndent + colored(e.ruleColor, strings.Repeat("─", ruleWidth))
		}

		leftDashes = min(max(leftDashes, 1), ruleWidth)
		leftPart := ""
		leftWidth := 0
		if left != "" {
			maxLeft := ruleWidth - leftDashes - 2
			if maxLeft < 1 {
				left = ""
			} else {
				left = ansi.Truncate(left, maxLeft, "…")
				leftPart = colored(e.ruleColor, strings.Repeat("─", leftDashes)) + " " + left + " "
				leftWidth = leftDashes + 2 + ansi.Width(left)
			}
		}

		rightPart := ""
		rightWidth := 0
		if right != "" {
			maxRight := ruleWidth - leftWidth - 1
			if maxRight >= 1 {
				right = ansi.Truncate(right, maxRight, "…")
				rightPart = " " + right
				rightWidth = ansi.Width(right) + 1
			}
		}

		middle := max(0, ruleWidth-leftWidth-rightWidth)
		return cellIndent + leftPart + colored(e.ruleColor, strings.Repeat("─", middle)) + rightPart
	}

	rows := e.rows(inner)

	cursorRow, cursorCol := e.cursorPosition(rows)

	if maxRows < 3 {
		maxRows = 3
	}
	attachments := chrome.Attachments
	if maxAttachments := max(0, maxRows-3); len(attachments) > maxAttachments {
		attachments = attachments[:maxAttachments]
	}
	attachmentGap := 0
	if len(attachments) > 0 && maxRows >= len(attachments)+4 {
		attachmentGap = 1
	}
	visible := max(maxRows-2-len(attachments)-attachmentGap, 1)

	if cursorRow < e.scroll {
		e.scroll = cursorRow
	}
	if cursorRow >= e.scroll+visible {
		e.scroll = cursorRow - visible + 1
	}
	if e.scroll > len(rows)-visible {
		e.scroll = len(rows) - visible
	}
	if e.scroll < 0 {
		e.scroll = 0
	}

	topLabel := chrome.TopLabel
	if e.scroll > 0 {
		if topLabel != "" {
			topLabel += dim(" · ")
		}
		topLabel += dim(dimLabel(e.scroll, "↑"))
	}
	bottomLeft := chrome.BottomLeft
	if hidden := len(rows) - e.scroll - visible; hidden > 0 {
		if bottomLeft != "" {
			bottomLeft += dim(" · ")
		}
		bottomLeft += dim(dimLabel(hidden, "↓"))
	}

	lines := []string{rule(topLabel, "", 3)}

	end := min(e.scroll+visible, len(rows))

	if len(e.value) == 0 {
		lines = append(lines, promptPrefix+fg(t.BrBlack)+e.placeholder+ansi.Reset)
	} else {
		for i, row := range rows[e.scroll:end] {
			prefix := continuationPrefix
			if e.scroll+i == 0 {
				prefix = promptPrefix
			}
			lines = append(lines, prefix+row.text)
		}
	}
	if attachmentGap > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, attachments...)

	lines = append(lines, rule(bottomLeft, chrome.BottomRight, 1))

	cursor := inline.Pos{
		Row: 1 + cursorRow - e.scroll,
		Col: len(cellIndent) + editorInset + cursorCol,
	}

	return lines, cursor
}

func dimLabel(n int, arrow string) string {
	return fmt.Sprintf("%s %d more", arrow, n)
}
