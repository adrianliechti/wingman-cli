package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/proxy"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

const (
	pageStart  = "start"
	pageList   = "list"
	pageDetail = "detail"
)

const listHeaderRows = 2

type App struct {
	p    *proxy.Proxy
	term *inline.Terminal

	queue chan func()
	quit  chan struct{}
	once  sync.Once

	activePage string
	selected   int
	selectedID string

	detailTitle  string
	detailLines  []string
	detailScroll int

	statusText   string
	seenRequests int
}

func newApp(p *proxy.Proxy) *App {
	return &App{
		p:          p,
		term:       inline.NewTerminal(),
		queue:      make(chan func(), 16),
		quit:       make(chan struct{}),
		activePage: pageStart,
	}
}

func (a *App) post(fn func()) {
	select {
	case a.queue <- fn:
	case <-a.quit:
	}
}

func (a *App) stop() {
	a.once.Do(func() { close(a.quit) })
}

func (a *App) Run() error {
	if err := a.term.Start(); err != nil {
		return err
	}
	a.term.EnterAlt()
	a.term.EnableMouse(true)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	a.render()

	for {
		select {
		case <-a.quit:
			a.term.Stop()
			return nil

		case fn := <-a.queue:
			fn()

		case <-ticker.C:
			entries := a.p.Store.List()

			if a.activePage == pageStart && len(entries) > 0 && a.seenRequests == 0 {
				a.activePage = pageList
			}
			a.seenRequests = len(entries)

		case ev := <-a.term.Events():
			switch ev := ev.(type) {
			case inline.ResizeEvent:
				a.term.Resized(ev.Width, ev.Height)
				if a.activePage == pageDetail {
					a.renderDetail()
				}
			case inline.MouseEvent:
				a.handleMouse(ev)
			case inline.KeyEvent:
				if a.handleKey(ev) {
					a.term.Stop()
					return nil
				}
			}
		}

		a.render()
	}
}

func (a *App) openDetail() {
	entries := a.p.Store.List()
	if idx := len(entries) - 1 - a.selected; idx >= 0 && idx < len(entries) {
		a.selectedID = entries[idx].ID
		a.renderDetail()
		a.activePage = pageDetail
	}
}

func (a *App) scrollDetail(delta, height int) {
	a.detailScroll += delta
	if maxScroll := len(a.detailLines) - (height - 3); a.detailScroll > maxScroll {
		a.detailScroll = maxScroll
	}
	if a.detailScroll < 0 {
		a.detailScroll = 0
	}
}

func (a *App) moveSelection(delta int) {
	entries := a.p.Store.List()
	a.selected += delta
	if a.selected >= len(entries) {
		a.selected = len(entries) - 1
	}
	if a.selected < 0 {
		a.selected = 0
	}
}

func (a *App) handleMouse(ev inline.MouseEvent) {
	_, height := a.term.Size()

	switch a.activePage {
	case pageList:
		switch ev.Kind {
		case inline.MouseWheel:
			a.moveSelection(ev.WheelDelta)
		case inline.MousePress:
			row := ev.Y - 1 - listHeaderRows
			if row >= 0 {
				start := a.listStart(height - 1)
				a.moveSelection(start + row - a.selected)
			}
		}

	case pageDetail:
		if ev.Kind == inline.MouseWheel {
			a.scrollDetail(ev.WheelDelta*3, height)
		}
	}
}

func (a *App) handleKey(ev inline.KeyEvent) bool {
	_, height := a.term.Size()
	entries := a.p.Store.List()

	quitKey := ev.Key == inline.KeyCtrl && ev.Rune == 'c' ||
		ev.Key == inline.KeyRune && ev.Rune == 'q'

	switch a.activePage {
	case pageStart:
		if quitKey || ev.Key == inline.KeyEsc {
			return true
		}

	case pageList:
		switch {
		case quitKey:
			return true
		case ev.Key == inline.KeyUp, ev.Key == inline.KeyRune && ev.Rune == 'k':
			a.moveSelection(-1)
		case ev.Key == inline.KeyDown, ev.Key == inline.KeyRune && ev.Rune == 'j':
			a.moveSelection(1)
		case ev.Key == inline.KeyPgUp:
			a.moveSelection(-(height - 4))
		case ev.Key == inline.KeyPgDn:
			a.moveSelection(height - 4)
		case ev.Key == inline.KeyHome, ev.Key == inline.KeyRune && ev.Rune == 'g':
			a.moveSelection(-len(entries))
		case ev.Key == inline.KeyEnd, ev.Key == inline.KeyRune && ev.Rune == 'G':
			a.moveSelection(len(entries))
		case ev.Key == inline.KeyEnter:
			a.openDetail()
		case ev.Key == inline.KeyRune && ev.Rune == 's':
			if idx := len(entries) - 1 - a.selected; idx >= 0 && idx < len(entries) {
				a.saveEntry(entries[idx])
			}
		}

	case pageDetail:
		switch {
		case quitKey:
			return true
		case ev.Key == inline.KeyEsc:
			a.activePage = pageList
		case ev.Key == inline.KeyUp, ev.Key == inline.KeyRune && ev.Rune == 'k':
			a.scrollDetail(-1, height)
		case ev.Key == inline.KeyDown, ev.Key == inline.KeyRune && ev.Rune == 'j':
			a.scrollDetail(1, height)
		case ev.Key == inline.KeyPgUp:
			a.scrollDetail(-(height - 3), height)
		case ev.Key == inline.KeyPgDn, ev.Key == inline.KeyRune && ev.Rune == ' ':
			a.scrollDetail(height-3, height)
		case ev.Key == inline.KeyHome, ev.Key == inline.KeyRune && ev.Rune == 'g':
			a.scrollDetail(-len(a.detailLines), height)
		case ev.Key == inline.KeyEnd, ev.Key == inline.KeyRune && ev.Rune == 'G':
			a.scrollDetail(len(a.detailLines), height)
		case ev.Key == inline.KeyRune && ev.Rune == 's':
			if entry, ok := a.p.Store.Get(a.selectedID); ok {
				a.saveEntry(entry)
			}
		}
	}

	return false
}

func dim(text string) string {
	return ansi.Fg(theme.Default.BrBlack) + text + ansi.Reset
}

func hint(key, label string) string {
	return dim(key) + " " + ansi.Fg(theme.Default.Foreground) + label + ansi.Reset
}

// footer renders key hints left and stats right, pinned to the last row by
// render().
func (a *App) footer(width int, hints []string, stats []string) string {
	t := theme.Default

	if a.statusText != "" {
		return " " + ansi.Fg(t.Red) + a.statusText + ansi.Reset
	}

	left := strings.Join(hints, dim("  "))
	right := strings.Join(stats, dim(" · "))

	gap := width - ansi.Width(left) - ansi.Width(right) - 2
	if gap < 1 {
		return ansi.Truncate(" "+left, width, "…")
	}

	return " " + left + strings.Repeat(" ", gap) + right
}

// padTo fills lines with blanks so the footer lands on the last row.
func padTo(lines []string, rows int) []string {
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines
}

func (a *App) render() {
	width, height := a.term.Size()
	if width <= 0 || height <= 0 {
		return
	}

	var lines []string

	switch a.activePage {
	case pageStart:
		lines = a.startLines(width, height)
	case pageList:
		lines = a.listLines(width, height-1)
		lines = padTo(lines, height-1)
		lines = append(lines, a.footer(width,
			[]string{hint("enter", "detail"), hint("s", "save"), hint("q", "quit")},
			a.stats()))
	case pageDetail:
		lines = a.detailPageLines(width, height-1)
		lines = padTo(lines, height-1)
		lines = append(lines, a.footer(width,
			[]string{hint("↑↓/jk", "scroll"), hint("s", "save"), hint("esc", "back"), hint("q", "quit")},
			a.stats()))
	}

	a.term.RenderAlt(lines, nil)
}

func (a *App) stats() []string {
	t := theme.Default

	entries := a.p.Store.List()
	inputTotal, outputTotal := a.p.Store.TotalTokens()

	stats := []string{dim(fmt.Sprintf("%d requests", len(entries)))}

	if inputTotal > 0 || outputTotal > 0 {
		stats = append(stats, dim(tui.FormatTokens(int64(inputTotal))+" in / "+tui.FormatTokens(int64(outputTotal))+" out"))
	}

	stats = append(stats, ansi.Fg(t.Cyan)+"⇆ http://"+a.p.Addr+ansi.Reset)

	return stats
}

func (a *App) startLines(width, height int) []string {
	t := theme.Default

	// Everything aligns to the logo's left edge.
	logoWidth := ansi.Width(tui.LogoLines[0])
	useLogo := width > logoWidth+2
	if !useLogo {
		logoWidth = len("wingman")
	}

	indent := max((width-logoWidth)/2, 0)
	at := func(text string) string {
		return strings.Repeat(" ", indent) + text
	}

	var block []string

	if useLogo {
		colors := []string{
			ansi.Fg(t.Blue), ansi.Fg(t.Cyan), ansi.Fg(t.Green), ansi.Fg(t.Yellow), ansi.Fg(t.Red), ansi.Fg(t.Magenta),
		}
		for i, l := range tui.LogoLines {
			block = append(block, at(colors[i%len(colors)]+l+ansi.Reset))
		}
	} else {
		block = append(block, at(ansi.Bold+"wingman"+ansi.Reset))
	}

	block = append(block, "")
	block = append(block, at(dim("point your OpenAI client at the proxy:")))
	block = append(block, "")
	block = append(block, at(ansi.Fg(t.Green)+"export OPENAI_BASE_URL=http://"+a.p.Addr+"/v1"+ansi.Reset))
	block = append(block, at(ansi.Fg(t.Green)+"export OPENAI_API_KEY=any-value"+ansi.Reset))

	pad := max((height-1-len(block))/2, 0)

	lines := make([]string, pad)
	lines = append(lines, block...)
	lines = padTo(lines, height-1)
	lines = append(lines, a.footer(width,
		[]string{hint("q", "quit")},
		a.stats()))

	return lines
}

// listStart returns the first visible row index for the current selection.
func (a *App) listStart(height int) int {
	rows := max(height-listHeaderRows, 1)
	if a.selected >= rows {
		return a.selected - rows + 1
	}
	return 0
}

func (a *App) listLines(width, height int) []string {
	t := theme.Default
	entries := a.p.Store.List()

	if a.selected >= len(entries) {
		a.selected = max(0, len(entries)-1)
	}

	pathWidth := max(width-8-7-5-7-22-7-7-9*2, 12)

	format := func(time_, method, path, status, dur, model, in, out string) string {
		return " " + ansi.Pad(time_, 8) + " " + ansi.Pad(method, 6) + " " +
			ansi.Pad(path, pathWidth) + " " + ansi.Pad(status, 4) + " " +
			ansi.Pad(dur, 6) + " " + ansi.Pad(model, 20) + " " +
			ansi.Pad(in, 6) + " " + ansi.Pad(out, 6)
	}

	lines := []string{
		"",
		dim(format("Time", "Method", "Path", "St", "Dur", "Model", "In", "Out")),
	}

	if len(entries) == 0 {
		lines = append(lines, "", " "+dim("no requests yet"))
		return lines
	}

	rows := height - listHeaderRows
	start := a.listStart(height)

	for i := start; i < len(entries) && i < start+rows; i++ {
		e := entries[len(entries)-1-i]

		statusColor := t.Green
		statusText := fmt.Sprintf("%d", e.Status)
		switch {
		case e.Status == 0:
			statusColor, statusText = t.Red, "ERR"
		case e.Status >= 500:
			statusColor = t.Red
		case e.Status >= 400:
			statusColor = t.Yellow
		}

		dur := fmt.Sprintf("%dms", e.Duration.Milliseconds())
		if e.Duration >= time.Second {
			dur = fmt.Sprintf("%.1fs", e.Duration.Seconds())
		}

		row := " " + dim(ansi.Pad(e.Timestamp.Format("15:04:05"), 8)) + " " +
			ansi.Fg(t.Magenta) + ansi.Pad(e.Method, 6) + ansi.Reset + " " +
			ansi.Pad(requestURLPathText(e.URL), pathWidth) + " " +
			ansi.Fg(statusColor) + ansi.Pad(statusText, 4) + ansi.Reset + " " +
			dim(ansi.Pad(dur, 6)) + " " +
			ansi.Fg(t.Cyan) + ansi.Pad(e.Model, 20) + ansi.Reset + " " +
			dim(ansi.Pad(tui.FormatTokens(int64(e.InputTokens)), 6)) + " " +
			dim(ansi.Pad(tui.FormatTokens(int64(e.OutputTokens)), 6))

		row = ansi.Truncate(row, width, "…")

		if i == a.selected {
			// Highlight re-applies the band across the row's own SGR resets.
			row = ansi.Highlight(ansi.Pad(row, width), 0, width, ansi.Bg(t.Selection))
		}

		lines = append(lines, row)
	}

	return lines
}

func (a *App) detailPageLines(width, height int) []string {
	percent := 100
	rows := height - 2
	if len(a.detailLines) > rows {
		percent = (a.detailScroll + rows) * 100 / len(a.detailLines)
	}

	pct := fmt.Sprintf(" %d%% ", percent)
	ruleWidth := max(width-2, 10)
	rule := strings.Repeat("─", max(1, ruleWidth-ansi.Width(pct)-2)) + pct + "──"

	lines := []string{
		" " + ansi.Bold + a.detailTitle + ansi.Reset,
		" " + dim(rule),
	}

	end := min(a.detailScroll+rows, len(a.detailLines))
	start := min(a.detailScroll, end)

	lines = append(lines, a.detailLines[start:end]...)

	return lines
}

func (a *App) renderDetail() {
	t := theme.Default
	a.detailScroll = 0

	entry, ok := a.p.Store.Get(a.selectedID)
	if !ok {
		a.detailTitle = "request"
		a.detailLines = []string{" " + ansi.Fg(t.Red) + "request not found" + ansi.Reset}
		return
	}

	a.detailTitle = entry.Method + " " + requestURLPathText(entry.URL)

	statusColor := t.Green
	if entry.Status >= 400 {
		statusColor = t.Yellow
	}
	if entry.Status >= 500 || entry.Status == 0 {
		statusColor = t.Red
	}

	var lines []string

	field := func(label string, value string) {
		lines = append(lines, " "+dim(ansi.Pad(label, 9))+" "+value)
	}

	lines = append(lines, "")
	field("URL", requestURLText(entry.URL))
	field("Status", ansi.Fg(statusColor)+fmt.Sprintf("%d", entry.Status)+ansi.Reset)
	field("Duration", entry.Duration.Round(time.Millisecond).String())
	field("Model", ansi.Fg(t.Cyan)+entry.Model+ansi.Reset)

	if entry.InputTokens > 0 || entry.OutputTokens > 0 {
		tokens := tui.FormatTokens(int64(entry.InputTokens)) + " in"
		if entry.CachedTokens > 0 {
			tokens += " (" + tui.FormatTokens(int64(entry.CachedTokens)) + " cached)"
		}
		tokens += " / " + tui.FormatTokens(int64(entry.OutputTokens)) + " out"
		field("Tokens", ansi.Fg(t.Cyan)+tokens+ansi.Reset)
	}

	if entry.Error != "" {
		field("Error", ansi.Fg(t.Red)+entry.Error+ansi.Reset)
	}

	width, _ := a.term.Size()
	if width <= 0 {
		width = 80
	}

	if len(entry.RequestBody) > 0 {
		lines = append(lines, "")
		lines = append(lines, " "+ansi.Bold+"request body"+ansi.Reset)
		lines = append(lines, "")
		lines = append(lines, formatJSON(entry.RequestBody)...)
	}

	if len(entry.ResponseBody) > 0 {
		lines = append(lines, "")
		lines = append(lines, " "+ansi.Bold+"response body"+ansi.Reset)
		lines = append(lines, "")

		if !looksLikeJSON(entry.ResponseBody) {
			lines = append(lines, formatSSEBody(entry.ResponseBody)...)
		} else {
			lines = append(lines, formatJSON(entry.ResponseBody)...)
		}
	}

	// RenderAlt requires lines within the terminal width; JSON string values
	// (chat payloads) routinely run to hundreds of columns.
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, ansi.Wrap(line, width-1)...)
	}

	a.detailLines = wrapped
}

func requestURLText(u *url.URL) string {
	if u == nil {
		return ""
	}

	return u.String()
}

func requestURLPathText(u *url.URL) string {
	if u == nil {
		return ""
	}

	return u.Path
}

func highlightLines(text, lang string) []string {
	var lines []string
	for line := range strings.SplitSeq(strings.TrimRight(markdown.Highlight(text, lang), "\n"), "\n") {
		lines = append(lines, " "+strings.ReplaceAll(line, "\t", "  "))
	}
	return lines
}

func formatJSON(data []byte) []string {
	var pretty bytes.Buffer

	if json.Indent(&pretty, data, "", "  ") == nil {
		return highlightLines(pretty.String(), "json")
	}

	return highlightLines(string(data), "")
}

func formatSSEBody(data []byte) []string {
	var lines []string

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if after, ok := strings.CutPrefix(line, "data: "); ok {
			payload := after
			if payload == "[DONE]" {
				lines = append(lines, " "+dim("data: [DONE]"))
				continue
			}

			var pretty bytes.Buffer
			if json.Indent(&pretty, []byte(payload), "", "  ") == nil {
				lines = append(lines, " "+dim("data:"))
				lines = append(lines, highlightLines(pretty.String(), "json")...)
			} else {
				lines = append(lines, highlightLines(line, "")...)
			}
		} else {
			lines = append(lines, " "+dim(line))
		}
	}

	return lines
}

func (a *App) saveEntry(entry proxy.RequestEntry) {
	name := fmt.Sprintf("%s.jsonl", entry.Timestamp.Format("20060102_150405"))

	if err := os.WriteFile(name, buildSavedEntry(entry), 0644); err != nil {
		a.statusText = fmt.Sprintf("Save failed: %v", err)
	}
}

func buildSavedEntry(entry proxy.RequestEntry) []byte {
	var buf strings.Builder

	for i, body := range [][]byte{entry.RequestBody, entry.ResponseBody} {
		if len(body) == 0 {
			continue
		}

		if i > 0 && buf.Len() > 0 {
			fmt.Fprint(&buf, "\n")
		}

		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "", "  ") == nil {
			fmt.Fprint(&buf, pretty.String())
		} else {
			buf.Write(body)
		}

		fmt.Fprint(&buf, "\n")
	}

	return []byte(buf.String())
}

func looksLikeJSON(data []byte) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}

	return false
}
