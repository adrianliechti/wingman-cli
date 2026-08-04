package code

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/model"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond

func formatElapsed(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%dh %02dm", secs/3600, (secs%3600)/60)
}

// statusLine renders the activity row above the composer; a blank line is
// reserved when idle so the layout never jumps.
func (a *App) statusLine(width int) string {
	t := theme.Default
	phase := a.getPhase()

	// While a question is pending the agent is waiting on the user; a
	// spinner would be misleading.
	if phase == PhaseIdle || a.promptActive || a.askActive {
		return ""
	}

	frame := spinnerFrames[a.spinnerFrame%len(spinnerFrames)]

	var label string
	var color = t.Cyan

	switch phase {
	case PhasePreparing:
		label, color = "Preparing", t.BrBlack
	case PhaseThinking, PhaseStreaming:
		label, color = "Thinking", t.Cyan
	case PhaseToolRunning:
		label, color = "Running", t.Yellow
	}

	line := colored(color, frame+" "+label)

	if phase != PhasePreparing && !a.phaseStart.IsZero() {
		line += " " + dim(fmt.Sprintf("(%s · esc to interrupt)", formatElapsed(time.Since(a.phaseStart))))
	}

	return cellIndent + ansi.Truncate(line, width-len(cellIndent), "…")
}

func (a *App) contextLeftPercent() (int, bool) {
	if a.lastInputTokens <= 0 {
		return 0, false
	}

	window := a.contextWindow
	if window <= 0 {
		_, currentModel := a.agent.Models(a.sessionID)
		window = int64(agent.ContextWindowFor(currentModel, false))
	}
	if window <= 0 {
		return 0, false
	}

	left := max(0, (window-a.lastInputTokens)*100/window)
	return int(left), true
}

// footerLine renders key hints left and session facts right, dropping hints
// from the right of the hint list until everything fits.
func (a *App) footerLine(width int) string {
	t := theme.Default

	var right []string

	var reg *task.Registry
	if provider, ok := a.agent.(taskProvider); ok {
		reg = provider.Tasks(a.sessionID)
	}
	if reg != nil {
		if running, _ := reg.Counts(); running == 1 {
			right = append(right, colored(t.Cyan, "1 agent"))
		} else if running > 1 {
			right = append(right, colored(t.Cyan, fmt.Sprintf("%d agents", running)))
		}
	}

	if a.inputTokens > 0 || a.outputTokens > 0 {
		tokens := fmt.Sprintf("↑%s ↓%s", tui.FormatTokens(a.inputTokens), tui.FormatTokens(a.outputTokens))
		right = append(right, dim(tokens))
	}

	if left, ok := a.contextLeftPercent(); ok {
		color := t.BrBlack
		switch {
		case left <= 10:
			color = t.Red
		case left <= 30:
			color = t.Yellow
		}
		right = append(right, colored(color, fmt.Sprintf("%d%% left", left)))
	}

	_, currentModel := a.agent.Models(a.sessionID)
	right = append(right, dim(model.Name(currentModel)))

	if effort, _ := a.agent.Effort(a.sessionID); effort != "" && effort != "auto" {
		right = append(right, dim(effort))
	}

	if a.currentMode == ModePlan {
		right = append(right, colored(t.Yellow, "plan"))
	}

	if name := a.agent.Name(); name != "" && name != code.BuiltinAgentName {
		right = append(right, dim(name))
	}

	rightText := strings.Join(right, dim(" · "))

	if a.footerHint != "" {
		hint := colored(t.Yellow, a.footerHint)
		gap := width - 2*len(cellIndent) - ansi.Width(hint) - ansi.Width(rightText)
		if gap >= 2 {
			return cellIndent + hint + strings.Repeat(" ", gap) + rightText
		}
		return cellIndent + ansi.Truncate(hint, width-len(cellIndent), "…")
	}

	var left []string

	if n := a.countPendingImages(); n == 1 {
		left = append(left, colored(t.Cyan, "1 image"))
	} else if n > 1 {
		left = append(left, colored(t.Cyan, fmt.Sprintf("%d images", n)))
	}

	if len(a.pendingFiles) == 1 {
		left = append(left, colored(t.Cyan, filepath.Base(a.pendingFiles[0])))
	} else if len(a.pendingFiles) > 1 {
		left = append(left, colored(t.Cyan, fmt.Sprintf("%d files", len(a.pendingFiles))))
	}

	hint := func(key, label string) string {
		return dim(key) + " " + colored(t.Foreground, label)
	}

	hints := []string{
		hint("/", "commands"),
		hint("@", "files"),
		hint("tab", "plan"),
		hint("shift+tab", "model"),
		hint("ctrl+o", "transcript"),
	}

	if a.currentMode == ModePlan {
		hints[2] = hint("tab", "agent")
	}

	sep := dim("  ")
	rightWidth := ansi.Width(rightText)

	for n := len(hints); n >= 0; n-- {
		parts := append(append([]string{}, left...), hints[:n]...)
		leftText := strings.Join(parts, sep)
		gap := width - 2*len(cellIndent) - ansi.Width(leftText) - rightWidth

		if gap >= 2 {
			return cellIndent + leftText + strings.Repeat(" ", gap) + rightText
		}
	}

	return cellIndent + ansi.Truncate(rightText, width-len(cellIndent), "…")
}
