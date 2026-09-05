package code

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/changes"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

const (
	// diffPanelBreakpoint is the terminal width from which the working tree
	// changes get their own pane beside the chat.
	diffPanelBreakpoint = 140
	diffPanelMinWidth   = 56
	diffPanelMaxWidth   = 110
	diffPanelPoll       = 2 * time.Second
	// maxDiffLineChars keeps one minified or base64 line from flooding the pane.
	maxDiffLineChars = 1000
)

// diffPanel is the side pane showing working tree changes next to the chat
// on wide terminals. Diffs load off the UI loop and refresh when the
// workspace change fingerprint moves.
type diffPanel struct {
	hidden bool

	diffs   []changes.FileDiff
	err     error
	loading bool

	fingerprint uint64
	checkedAt   time.Time

	header     string
	lines      []string
	sections   []diffSection
	linesWidth int
	offset     int
	rows       int
}

// diffSection marks where a file's name row sits in the scrollable lines so
// the name can stay pinned while its hunks scroll.
type diffSection struct {
	path    string
	nameRow int
}

func (a *App) diffPanelWidth(termWidth int) int {
	if a.diffPanel.hidden || termWidth < diffPanelBreakpoint || len(a.diffPanel.diffs) == 0 {
		return 0
	}
	return min(diffPanelMaxWidth, max(diffPanelMinWidth, termWidth*42/100))
}

// toggleDiffPanel hides or shows the pane on wide terminals; narrow
// terminals keep the full-screen overlay instead.
func (a *App) toggleDiffPanel() bool {
	w, _ := a.term.Size()
	if w < diffPanelBreakpoint {
		return false
	}
	a.diffPanel.hidden = !a.diffPanel.hidden
	if a.diffPanel.hidden {
		a.showToast("Diff panel hidden", theme.Default.BrBlack)
	} else if a.agent.Workspace().HasChanges() {
		a.pollDiffPanel(true)
	} else {
		a.showToast("Change tracking is still starting", theme.Default.Yellow)
	}
	a.invalidate()
	return true
}

// pollDiffPanel checks the change fingerprint in the background and reloads
// the diffs when it moved. Nothing runs while the pane cannot be shown.
func (a *App) pollDiffPanel(force bool) {
	p := &a.diffPanel
	if a.term == nil || p.hidden || p.loading {
		return
	}
	if w, _ := a.term.Size(); w < diffPanelBreakpoint {
		return
	}
	if !force && time.Since(p.checkedAt) < diffPanelPoll {
		return
	}
	workspace := a.agent.Workspace()
	if !workspace.HasChanges() {
		return
	}

	p.loading = true
	p.checkedAt = time.Now()
	previous := p.fingerprint
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	go func() {
		fingerprint := workspace.ChangesFingerprint(ctx)
		if !force && fingerprint == previous {
			a.post(func() { p.loading = false })
			return
		}
		diffs, err := workspace.Diffs(ctx)
		a.post(func() {
			p.loading = false
			p.fingerprint = fingerprint
			p.diffs, p.err = diffs, err
			p.lines = nil
			switch {
			case force && err != nil:
				a.showToast(err.Error(), theme.Default.Yellow)
			case force && len(diffs) == 0:
				a.showToast("Working tree clean", theme.Default.BrBlack)
			}
			a.invalidate()
		})
	}()
}

func (a *App) scrollDiffPanel(delta int) {
	p := &a.diffPanel
	p.offset += delta
	p.offset = min(p.offset, max(0, len(p.lines)-p.rows))
	p.offset = max(p.offset, 0)
	a.invalidate()
}

// render pins the summary on the first row and, once a file's own name row
// has scrolled away, that file's name above its hunks.
func (p *diffPanel) render(width, height int) []string {
	if p.lines == nil || p.linesWidth != width {
		p.header, p.lines, p.sections = renderDiffPanelLines(p.diffs, p.err, width-1)
		p.linesWidth = width
	}
	content := max(1, width-1)
	rows := max(0, height-1)
	p.rows = rows
	p.offset = min(p.offset, max(0, len(p.lines)-rows))
	p.offset = max(p.offset, 0)

	pin := func(line string) string { return ansi.Pad(ansi.Truncate(line, width, "…"), width) }
	out := []string{pin(p.header)}

	sticky := ""
	for _, section := range p.sections {
		if section.nameRow < p.offset {
			sticky = section.path
		}
	}
	if sticky != "" && rows > 2 {
		out = append(out, pin(bold(sticky)), pin(colored(theme.Default.BrBlack, strings.Repeat("─", width))))
		rows -= 2
	}

	for row := range rows {
		line := ""
		if index := p.offset + row; index < len(p.lines) {
			line = p.lines[index]
		}
		marker := scrollMarker(row, rows, p.offset, len(p.lines))
		out = append(out, ansi.Pad(ansi.Truncate(line, content, "…"), content)+marker)
	}
	return out[:min(len(out), height)]
}

func renderDiffPanelLines(diffs []changes.FileDiff, err error, width int) (header string, lines []string, sections []diffSection) {
	t := theme.Default
	width = max(width, 20)

	if err != nil {
		return colored(t.Yellow, ansi.Truncate(err.Error(), width, "…")), nil, nil
	}

	var totalInsertions, totalDeletions int
	stats := make([][2]int, len(diffs))
	for i, diff := range diffs {
		ins, del := countDiffStats(diff.Patch)
		stats[i] = [2]int{ins, del}
		totalInsertions += ins
		totalDeletions += del
	}

	noun := "files"
	if len(diffs) == 1 {
		noun = "file"
	}
	header = bold(fmt.Sprintf("%d %s changed", len(diffs), noun)) + " " + diffStatText(totalInsertions, totalDeletions)
	lines = []string{""}

	for i, diff := range diffs {
		stat := diffStatText(stats[i][0], stats[i][1])
		path := ansi.Truncate(colored(diffStatusColor(diff.Status), diff.Path), max(10, width-ansi.Width(stat)-1), "…")
		gap := max(1, width-ansi.Width(path)-ansi.Width(stat))
		lines = append(lines, path+strings.Repeat(" ", gap)+stat)
	}

	for _, diff := range diffs {
		rule := colored(t.BrBlack, strings.Repeat("─", width))
		lines = append(lines, "", rule)
		sections = append(sections, diffSection{path: diff.Path, nameRow: len(lines)})
		lines = append(lines, bold(ansi.Truncate(diff.Path, width, "…")), "")
		lines = append(lines, renderDiffPatchLines(diff.Patch, width)...)
	}

	return header, lines, sections
}

func diffStatText(insertions, deletions int) string {
	t := theme.Default
	return colored(t.Green, "+"+strconv.Itoa(insertions)) + " " + colored(t.Red, "-"+strconv.Itoa(deletions))
}

func diffStatusColor(status changes.FileStatus) ansi.Color {
	t := theme.Default
	switch status {
	case changes.StatusAdded:
		return t.Green
	case changes.StatusDeleted:
		return t.Red
	default:
		return t.BrBlack
	}
}

// renderDiffPatchLines renders hunk bodies with file line numbers in a
// gutter: new-file numbers for context and additions, old-file numbers for
// deletions. Wrapped continuations repeat the change marker.
func renderDiffPatchLines(patch string, width int) []string {
	t := theme.Default
	const gutter = 6 // "%4d " + marker
	inner := max(10, width-gutter)

	var lines []string
	oldLine, newLine := 0, 0
	inHunk := false

	for line := range strings.SplitSeq(strings.TrimRight(patch, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			oldLine, newLine = parseHunkHeader(line)
			if inHunk {
				lines = append(lines, dim(strings.Repeat("┄", min(width, 8))))
			}
			inHunk = true
			continue
		case !inHunk, strings.HasPrefix(line, "\\ "):
			continue
		}

		marker, number, color := ' ', 0, ansi.Color{}
		body := line
		if len(line) > 0 {
			body = line[1:]
			switch line[0] {
			case '+':
				marker, number, color = '+', newLine, t.Green
				newLine++
			case '-':
				marker, number, color = '-', oldLine, t.Red
				oldLine++
			default:
				number = newLine
				oldLine++
				newLine++
			}
		} else {
			number = newLine
			oldLine++
			newLine++
		}

		style := func(text string) string { return text }
		if marker != ' ' {
			style = func(text string) string { return colored(color, text) }
		}

		body = expandTabs(body)
		if total := ansi.Width(body); total > maxDiffLineChars {
			body = ansi.CutPlain(body, 0, maxDiffLineChars) + fmt.Sprintf(" … [%d more chars]", total-maxDiffLineChars)
		}
		for i, part := range hardWrap(body, inner) {
			prefix := strings.Repeat(" ", 4)
			if i == 0 {
				prefix = fmt.Sprintf("%4d", number)
			}
			lines = append(lines, dim(prefix)+" "+style(string(marker)+part))
		}
	}

	return lines
}

func expandTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "    ")
}

// hardWrap splits plain text into display-width chunks; code keeps its shape
// better than with word wrapping.
func hardWrap(text string, width int) []string {
	total := ansi.Width(text)
	if total <= width {
		return []string{text}
	}
	var parts []string
	for col := 0; col < total; col += width {
		parts = append(parts, ansi.CutPlain(text, col, col+width))
	}
	return parts
}

// parseHunkHeader returns the starting old and new line numbers of an
// `@@ -a,b +c,d @@` header.
func parseHunkHeader(header string) (oldStart, newStart int) {
	fields := strings.Fields(header)
	if len(fields) < 3 {
		return 1, 1
	}
	parse := func(field string) int {
		field = strings.TrimLeft(field, "-+")
		field, _, _ = strings.Cut(field, ",")
		n, err := strconv.Atoi(field)
		if err != nil || n <= 0 {
			return 1
		}
		return n
	}
	return parse(fields[1]), parse(fields[2])
}
