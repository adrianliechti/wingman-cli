package claw

import (
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

const (
	indent       = "  "
	sidebarWidth = 24
)

func dim(text string) string {
	return ansi.Fg(theme.Default.BrBlack) + text + ansi.Reset
}

func (t *TUI) chatWidth() int {
	w, _ := t.term.Size()
	cw := w - sidebarWidth - 1 - len(indent)
	if cw < 40 {
		return 40
	}
	return cw
}

func (t *TUI) handleKey(ev inline.KeyEvent) bool {
	switch ev.Key {
	case inline.KeyCtrl:
		if ev.Rune == 'c' {
			return true
		}

	case inline.KeyTab:
		if t.focus == focusInput {
			t.focus = focusAgents
		} else {
			t.focus = focusInput
		}
		return false

	case inline.KeyUp:
		if t.focus == focusAgents {
			if t.agentIndex > 0 {
				t.agentIndex--
			}
		}
		return false

	case inline.KeyDown:
		if t.focus == focusAgents {
			if t.agentIndex < len(t.agentNames)-1 {
				t.agentIndex++
			}
		}
		return false

	case inline.KeyPgUp:
		t.follow = false
		t.chatScroll -= 10
		if t.chatScroll < 0 {
			t.chatScroll = 0
		}
		return false

	case inline.KeyPgDn:
		t.chatScroll += 10
		if t.chatScroll >= len(t.chatLines) {
			t.chatScroll = len(t.chatLines)
			t.follow = true
		}
		return false

	case inline.KeyEnter:
		if t.focus == focusAgents {
			if t.agentIndex >= 0 && t.agentIndex < len(t.agentNames) {
				t.selectAgent(t.agentNames[t.agentIndex])
			}
			t.focus = focusInput
			return false
		}
		t.submitInput()
		return false

	case inline.KeyBackspace:
		if t.focus == focusInput && t.inputCursor > 0 {
			t.input = append(t.input[:t.inputCursor-1], t.input[t.inputCursor:]...)
			t.inputCursor--
		}
		return false

	case inline.KeyDelete:
		if t.focus == focusInput && t.inputCursor < len(t.input) {
			t.input = append(t.input[:t.inputCursor], t.input[t.inputCursor+1:]...)
		}
		return false

	case inline.KeyLeft:
		if t.focus == focusInput && t.inputCursor > 0 {
			t.inputCursor--
		}
		return false

	case inline.KeyRight:
		if t.focus == focusInput && t.inputCursor < len(t.input) {
			t.inputCursor++
		}
		return false

	case inline.KeyHome:
		t.inputCursor = 0
		return false

	case inline.KeyEnd:
		t.inputCursor = len(t.input)
		return false

	case inline.KeyRune:
		if t.focus == focusInput && !ev.Alt {
			t.input = append(t.input[:t.inputCursor], append([]rune{ev.Rune}, t.input[t.inputCursor:]...)...)
			t.inputCursor++
		}
		return false
	}

	return false
}

func (t *TUI) insertInput(text string) {
	runes := []rune(text)
	t.input = append(t.input[:t.inputCursor], append(runes, t.input[t.inputCursor:]...)...)
	t.inputCursor += len(runes)
}

// render composes the full-screen dashboard frame.
func (t *TUI) render() {
	th := theme.Default
	width, height := t.term.Size()
	if width <= 0 || height <= 0 {
		return
	}

	sep := ansi.Fg(th.BrBlack) + "│" + ansi.Reset

	taskRows := len(t.taskLines) + 2
	if max := height / 3; taskRows > max {
		taskRows = max
	}
	if taskRows < 3 {
		taskRows = 3
	}

	chatRows := max(height-taskRows-1-2, 3)

	// Left column: agents list.
	left := make([]string, height)
	if height > 2 {
		left[1] = indent + ansi.Fg(th.Cyan) + ansi.Bold + "Agents" + ansi.Reset
	}

	for i, name := range t.agentNames {
		row := 3 + i
		if row >= height {
			break
		}
		label := name
		if t.isBusy(name) {
			label += " " + ansi.Fg(th.Yellow) + "…" + ansi.Reset
		}
		switch {
		case i == t.agentIndex && t.focus == focusAgents:
			left[row] = indent + ansi.Fg(th.Cyan) + "→ " + label + ansi.Reset
		case name == t.selected():
			left[row] = indent + ansi.Fg(th.Cyan) + "● " + ansi.Reset + label
		default:
			left[row] = indent + "  " + label
		}
	}

	// Right column: tasks, rule, chat, input, status.
	var right []string

	right = append(right, "")
	right = append(right, indent+ansi.Fg(th.Yellow)+ansi.Bold+"Tasks"+ansi.Reset)
	right = append(right, "")

	taskBudget := taskRows - 3
	for i := 0; i < taskBudget && i < len(t.taskLines); i++ {
		right = append(right, t.taskLines[i])
	}
	for len(right) < taskRows {
		right = append(right, "")
	}

	rightWidth := width - sidebarWidth - 1
	right = append(right, ansi.Fg(th.BrBlack)+strings.Repeat("─", max(1, rightWidth))+ansi.Reset)

	if t.chatScroll > len(t.chatLines) {
		t.chatScroll = len(t.chatLines)
	}

	start := max(min(t.chatScroll-chatRows, len(t.chatLines)-chatRows), 0)
	for i := range chatRows {
		if start+i < len(t.chatLines) {
			right = append(right, t.chatLines[start+i])
		} else {
			right = append(right, "")
		}
	}

	inputText := string(t.input)
	if t.focus == focusInput {
		runes := t.input
		before := string(runes[:t.inputCursor])
		at := " "
		after := ""
		if t.inputCursor < len(runes) {
			at = string(runes[t.inputCursor])
			after = string(runes[t.inputCursor+1:])
		}
		inputText = before + ansi.Reverse + at + ansi.Reset + after
	}
	right = append(right, indent+ansi.Fg(th.Cyan)+"❯ "+ansi.Reset+inputText)
	right = append(right, t.statusLine(rightWidth))

	// Merge columns.
	frame := make([]string, height)
	for i := range height {
		l := ""
		if i < len(left) {
			l = left[i]
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		frame[i] = ansi.Pad(l, sidebarWidth) + sep + ansi.Truncate(r, rightWidth, "…")
	}

	t.term.RenderAlt(frame, nil)
}

func (t *TUI) statusLine(width int) string {
	th := theme.Default
	name := t.selected()
	_, usage, ok := t.claw.AgentState(name)

	var parts []string

	if t.isBusy(name) {
		parts = append(parts, ansi.Fg(th.Yellow)+"working…"+ansi.Reset)
	}

	if ok {
		tokens := "↑" + tui.FormatTokens(usage.InputTokens)
		if usage.CachedTokens > 0 {
			tokens += " (" + tui.FormatTokens(usage.CachedTokens) + " cached)"
		}
		tokens += " ↓" + tui.FormatTokens(usage.OutputTokens)
		parts = append(parts, dim(tokens))
	}

	parts = append(parts, ansi.Fg(th.Cyan)+name+ansi.Reset)

	text := strings.Join(parts, dim(" · "))
	gap := max(width-ansi.Width(text)-2, 0)

	return strings.Repeat(" ", gap) + text
}
