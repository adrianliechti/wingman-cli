package code

import (
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

// Overlay is a full-screen view replacing the chat frame. HandleKey returns
// true when the overlay should close.
type Overlay interface {
	HandleKey(ev inline.KeyEvent) bool
	Render(width, height int) []string
}

func (a *App) openOverlay(o Overlay) {
	a.overlay = o
	a.invalidate()
}

func (a *App) closeOverlay() {
	a.overlay = nil
	a.invalidate()
}

// pager is a scrollable line view with a title header and hint footer.
type pager struct {
	title string
	lines []string
	hints string

	offset int
}

func (p *pager) contentRows(height int) int {
	rows := max(height-3, 0)
	return rows
}

func (p *pager) clamp(height int) {
	maxOffset := len(p.lines) - p.contentRows(height)
	if p.offset > maxOffset {
		p.offset = maxOffset
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

func (p *pager) HandleKey(ev inline.KeyEvent, height int) bool {
	switch ev.Key {
	case inline.KeyEsc:
		return true
	case inline.KeyUp:
		p.offset--
	case inline.KeyDown:
		p.offset++
	case inline.KeyPgUp:
		p.offset -= p.contentRows(height)
	case inline.KeyPgDn:
		p.offset += p.contentRows(height)
	case inline.KeyHome:
		p.offset = 0
	case inline.KeyEnd:
		p.offset = len(p.lines)
	case inline.KeyCtrl:
		if ev.Rune == 'c' {
			return true
		}
	case inline.KeyRune:
		switch ev.Rune {
		case 'q':
			return true
		case 'j':
			p.offset++
		case 'k':
			p.offset--
		case 'g':
			p.offset = 0
		case 'G':
			p.offset = len(p.lines)
		}
	}

	p.clamp(height)
	return false
}

func (p *pager) HandleMouse(ev inline.MouseEvent, height int) {
	if ev.Kind == inline.MouseWheel {
		p.offset += ev.WheelDelta * 3
		p.clamp(height)
	}
}

func (p *pager) Render(width, height int) []string {
	t := theme.Default
	p.clamp(height)

	percent := 100
	rows := p.contentRows(height)
	if len(p.lines) > rows {
		percent = (p.offset + rows) * 100 / len(p.lines)
	}

	head := cellIndent + bold(p.title)
	ruleWidth := max(width-2*len(cellIndent), 10)
	pct := fmt.Sprintf(" %d%% ", percent)
	rule := strings.Repeat("─", ruleWidth-ansi.Width(pct)-2) + pct + "──"

	lines := []string{head, cellIndent + colored(t.BrBlack, rule)}

	end := min(p.offset+rows, len(p.lines))

	// Lines were wrapped at open time; a narrower terminal since then must
	// not leak over-wide lines into the renderer.
	for _, line := range p.lines[p.offset:end] {
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}

	for len(lines) < height-1 {
		lines = append(lines, "")
	}

	lines = append(lines, cellIndent+p.hints)

	return lines
}

// transcriptLines renders committed messages with nothing truncated. A nil
// isToolHidden shows every tool result.
func transcriptLines(messages []agent.Message, width int, isToolHidden func(string) bool) []string {
	var lines []string

	for _, msg := range messages {
		if msg.Hidden {
			continue
		}
		for _, c := range msg.Content {
			if c.Hidden {
				continue
			}
			switch {
			case c.ToolResult != nil:
				if isToolHidden != nil && isToolHidden(c.ToolResult.Name) {
					continue
				}
				lines = append(lines, cellTool(c.ToolResult, width, true)...)
			case c.Reasoning != nil && c.Reasoning.Summary != "":
				lines = append(lines, cellReasoning(c.Reasoning.Summary, width, true)...)
				lines = append(lines, "")
			case c.Text != "":
				switch msg.Role {
				case agent.RoleUser:
					if isCommandEcho(c.Text) {
						lines = append(lines, cellCommand(c.Text, width)...)
					} else {
						lines = append(lines, cellUser(c.Text, width)...)
					}
				case agent.RoleAssistant:
					lines = append(lines, cellAssistant(c.Text, width, theme.Default.Green)...)
				}
			}
		}
	}

	return lines
}

// taskOverlay is a live window onto a background agent's transcript: while
// the task runs it re-snapshots the messages (throttled) and follows the
// bottom until the user scrolls away.
type taskOverlay struct {
	task   *task.Task
	pager  pager
	height int
	follow bool

	builtAt     time.Time
	builtWidth  int
	builtStatus task.Status
}

func (a *App) showTaskPeek(t *task.Task) {
	o := &taskOverlay{
		task:   t,
		follow: true,
	}
	o.pager.hints = dim("↑↓/jk/wheel scroll · g/G top/bottom · esc close")
	a.openOverlay(o)
}

func (o *taskOverlay) rebuild(width int, status task.Status) {
	title := fmt.Sprintf("agent %s · %s · %s · %s", o.task.ID, o.task.AgentType, status, o.task.Elapsed().Round(time.Second))
	if status == task.StatusRunning {
		if activity := o.task.Activity(); activity != "" {
			title += " · " + activity
		}
	}
	o.pager.title = title

	lines := transcriptLines(o.task.PeekMessages(), width, nil)
	if len(lines) == 0 {
		lines = []string{cellIndent + dim("No output yet…")}
	}
	o.pager.lines = lines
	if o.follow {
		o.pager.offset = len(lines)
	}
}

const taskPeekRefresh = 500 * time.Millisecond

func (o *taskOverlay) Render(width, height int) []string {
	o.height = height

	status := o.task.Status()
	if o.pager.lines == nil || width != o.builtWidth || status != o.builtStatus ||
		(status == task.StatusRunning && time.Since(o.builtAt) > taskPeekRefresh) {
		o.rebuild(width, status)
		o.builtAt = time.Now()
		o.builtWidth = width
		o.builtStatus = status
	}

	return o.pager.Render(width, height)
}

func (o *taskOverlay) HandleKey(ev inline.KeyEvent) bool {
	switch {
	case ev.Key == inline.KeyEnd, ev.Key == inline.KeyRune && ev.Rune == 'G':
		o.follow = true
	case ev.Key == inline.KeyUp, ev.Key == inline.KeyPgUp, ev.Key == inline.KeyHome,
		ev.Key == inline.KeyRune && (ev.Rune == 'k' || ev.Rune == 'g'):
		o.follow = false
	}
	return o.pager.HandleKey(ev, o.height)
}

func (o *taskOverlay) HandleMouse(ev inline.MouseEvent) {
	if ev.Kind == inline.MouseWheel && ev.WheelDelta < 0 {
		o.follow = false
	}
	o.pager.HandleMouse(ev, o.height)
}
