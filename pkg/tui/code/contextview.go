package code

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (a *App) showContextStats() {
	provider, supported := a.agent.(contextStatsProvider)
	if !supported {
		a.showToast("Context statistics are unavailable for this agent", theme.Default.Yellow)
		return
	}

	id := a.sessionID

	go func() {
		stats, ok := provider.ContextStats(id)

		a.post(func() {
			if a.sessionID != id {
				return
			}
			if !ok {
				a.showToast("No active session", theme.Default.Yellow)
			} else {
				a.appendChat(cellContextStats(stats, a.width()))
			}
			a.invalidate()
		})
	}()
}

const (
	contextGridCols = 20
	contextGridRows = 10
)

type contextCategory struct {
	label  string
	tokens int64
	color  ansi.Color
}

func cellContextStats(stats agent.ContextStats, width int) []string {
	t := theme.Default

	window := int64(stats.Window)
	if window <= 0 {
		window = 1
	}

	categories := []contextCategory{
		{"System prompt", int64(stats.InstructionsTokens), t.Blue},
		{fmt.Sprintf("Tools (%d)", len(stats.ToolStats)), int64(stats.ToolsTokens), t.Yellow},
		{fmt.Sprintf("Messages (%d)", stats.MessageCount), int64(stats.MessagesTokens), t.Magenta},
	}

	current := stats.LastInputTokens
	if current <= 0 {
		current = int64(stats.EstimatedTotal())
	}
	free := max(window-current, 0)

	pct := func(tokens int64) string {
		return fmt.Sprintf("%.1f%%", float64(tokens)*100/float64(window))
	}

	var legend []string
	legend = append(legend,
		bold("Context Usage"),
		dim(stats.Model),
		fmt.Sprintf("%s/%s tokens (%s)", tui.FormatTokens(current), tui.FormatTokens(window), pct(current)),
		"",
		dim("Estimated usage by category"),
	)
	for _, c := range categories {
		legend = append(legend, fmt.Sprintf("%s %s: %s (%s)", colored(c.color, "■"), c.label, tui.FormatTokens(c.tokens), pct(c.tokens)))
	}
	legend = append(legend, fmt.Sprintf("%s Free space: %s (%s)", dim("□"), tui.FormatTokens(free), pct(free)))

	var footer []string
	if len(stats.ToolStats) > 0 {
		var parts []string
		for _, ts := range stats.ToolStats[:min(5, len(stats.ToolStats))] {
			parts = append(parts, fmt.Sprintf("%s ~%s", ts.Name, tui.FormatTokens(int64(ts.Tokens))))
		}
		footer = append(footer, "", dim("largest tools: "+strings.Join(parts, " · ")))
	}

	gridWidth := contextGridCols*2 - 1
	if width < len(cellIndent)+gridWidth+24 {
		lines := []string{""}
		for _, l := range legend {
			lines = append(lines, cellIndent+l)
		}
		for _, l := range footer {
			if l != "" {
				l = cellIndent + l
			}
			lines = append(lines, l)
		}
		lines = append(lines, "")
		return lines
	}

	grid := contextGrid(categories, current, window)

	lines := []string{""}
	for i := 0; i < max(len(grid), len(legend)); i++ {
		row := strings.Repeat(" ", gridWidth)
		if i < len(grid) {
			row = grid[i]
		}
		entry := ""
		if i < len(legend) {
			entry = legend[i]
		}
		lines = append(lines, cellIndent+row+ansi.Reset+"   "+entry+ansi.Reset)
	}
	for _, l := range footer {
		if l != "" {
			l = cellIndent + l
		}
		lines = append(lines, l)
	}
	lines = append(lines, "")

	return lines
}

// contextGrid renders proportional cells: one glyph per 1/(rows*cols) of the
// window, colored per category, dim squares for free space.
func contextGrid(categories []contextCategory, current, window int64) []string {
	total := contextGridCols * contextGridRows

	var cells []string
	for _, c := range categories {
		n := int(c.tokens * int64(total) / window)
		if n == 0 && c.tokens > 0 {
			n = 1
		}
		for range n {
			cells = append(cells, colored(c.color, "■"))
		}
	}

	// Conversation growth beyond the itemized estimates still occupies cells.
	if used := int(current * int64(total) / window); used > len(cells) {
		for range used - len(cells) {
			cells = append(cells, colored(theme.Default.BrBlack, "■"))
		}
	}

	if len(cells) > total {
		cells = cells[:total]
	}
	for len(cells) < total {
		cells = append(cells, dim("□"))
	}

	var rows []string
	for r := 0; r < contextGridRows; r++ {
		rows = append(rows, strings.Join(cells[r*contextGridCols:(r+1)*contextGridCols], " "))
	}
	return rows
}
