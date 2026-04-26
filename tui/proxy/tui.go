package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/proxy"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	pageStart  = "start"
	pageList   = "list"
	pageDetail = "detail"
)

type App struct {
	app   *tview.Application
	pages *tview.Pages
	p     *proxy.Proxy

	statusBar *tview.TextView
	table     *tview.Table
	detail    *tview.TextView
	startView *tview.TextView

	selectedID string
	activePage string

	seenRequests int
}

func newApp(p *proxy.Proxy) *App {
	a := &App{
		app:        tview.NewApplication(),
		p:          p,
		activePage: pageStart,
	}

	a.build()
	return a
}

func (a *App) build() {
	th := theme.Default

	a.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		screen.Clear()
		return false
	})

	// Status bar
	a.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	a.statusBar.SetBackgroundColor(tcell.ColorDefault)
	a.updateStatusBar()

	// Start page
	a.startView = tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true)
	a.startView.SetBackgroundColor(tcell.ColorDefault)
	a.startView.SetText(a.startPageContent())

	// Request list table
	a.table = tview.NewTable().
		SetSelectable(true, false).
		SetFixed(1, 0)
	a.table.SetBackgroundColor(tcell.ColorDefault)
	a.table.SetSelectedStyle(tcell.StyleDefault.
		Background(th.Selection).
		Foreground(th.Foreground))
	a.renderTable()

	// Detail view
	a.detail = tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(true)
	a.detail.SetBackgroundColor(tcell.ColorDefault)

	// Pages
	a.pages = tview.NewPages()
	a.pages.SetBackgroundColor(tcell.ColorDefault)

	startLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	startLayout.SetBackgroundColor(tcell.ColorDefault)
	startLayout.AddItem(a.startView, 0, 1, true)

	listLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	listLayout.SetBackgroundColor(tcell.ColorDefault)
	listLayout.AddItem(a.table, 0, 1, true)
	listLayout.AddItem(a.statusBar, 1, 0, false)

	detailLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	detailLayout.SetBackgroundColor(tcell.ColorDefault)
	detailLayout.AddItem(a.detail, 0, 1, true)
	detailLayout.AddItem(a.statusBar, 1, 0, false)

	a.pages.AddPage(pageStart, startLayout, true, true)
	a.pages.AddPage(pageList, listLayout, true, false)
	a.pages.AddPage(pageDetail, detailLayout, true, false)

	// Input handling
	a.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch a.activePage {
		case pageStart:
			switch event.Key() {
			case tcell.KeyEnter:
				a.switchTo(pageList)
				return nil
			case tcell.KeyEscape, tcell.KeyCtrlC:
				a.app.Stop()
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'l' || event.Rune() == 'q' {
					if event.Rune() == 'q' {
						a.app.Stop()
						return nil
					}
					a.switchTo(pageList)
					return nil
				}
			}

		case pageList:
			switch event.Key() {
			case tcell.KeyEnter:
				row, _ := a.table.GetSelection()
				if row > 0 {
					entries := a.p.Store.List()
					idx := len(entries) - row // newest first, row 0 is header
					if idx >= 0 && idx < len(entries) {
						a.selectedID = entries[idx].ID
						a.renderDetail()
						a.switchTo(pageDetail)
					}
				}
				return nil
			case tcell.KeyCtrlC:
				a.app.Stop()
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'q' {
					a.app.Stop()
					return nil
				}
				if event.Rune() == 's' {
					row, _ := a.table.GetSelection()
					if row > 0 {
						entries := a.p.Store.List()
						idx := len(entries) - row
						if idx >= 0 && idx < len(entries) {
							a.saveEntry(entries[idx])
						}
					}
					return nil
				}
			}

		case pageDetail:
			switch event.Key() {
			case tcell.KeyEscape:
				a.switchTo(pageList)
				return nil
			case tcell.KeyCtrlC:
				a.app.Stop()
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'q' {
					a.app.Stop()
					return nil
				}
				if event.Rune() == 's' {
					if entry, ok := a.p.Store.Get(a.selectedID); ok {
						a.saveEntry(entry)
					}
					return nil
				}
				if event.Rune() == ' ' {
					row, col := a.detail.GetScrollOffset()
					_, _, _, height := a.detail.GetInnerRect()
					a.detail.ScrollTo(row+height, col)
					return nil
				}
			}
		}

		return event
	})

	a.app.SetRoot(a.pages, true)
}

func (a *App) switchTo(page string) {
	a.activePage = page
	a.pages.SwitchToPage(page)

	switch page {
	case pageStart:
		a.app.SetFocus(a.startView)
	case pageList:
		a.renderTable()
		a.app.SetFocus(a.table)
	case pageDetail:
		a.app.SetFocus(a.detail)
	}
}

func (a *App) Run() error {
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			a.app.QueueUpdateDraw(func() {
				entries := a.p.Store.List()

				// Auto-switch to list on first request
				if a.activePage == pageStart && len(entries) > 0 && a.seenRequests == 0 {
					a.seenRequests = len(entries)
					a.switchTo(pageList)
				} else {
					a.seenRequests = len(entries)
				}

				if a.activePage == pageList {
					a.renderTable()
				}

				a.updateStatusBar()
			})
		}
	}()

	return a.app.Run()
}

func (a *App) Stop() {
	a.app.Stop()
}

func (a *App) startPageContent() string {
	th := theme.Default

	var b strings.Builder

	fmt.Fprint(&b, strings.ReplaceAll(tui.Logo, "\n", "\n  "))
	fmt.Fprint(&b, "\n")

	fmt.Fprintf(&b, "  [%s::b]Listening[-::-]  [%s]http://%s[-]\n\n", th.Yellow, th.Foreground, a.p.Addr)

	fmt.Fprintf(&b, "  [%s::b]Usage[-::-]\n", th.Cyan)
	fmt.Fprintf(&b, "  [%s]Point your OpenAI client to the proxy:[-]\n\n", th.BrBlack)
	fmt.Fprintf(&b, "  [%s]export OPENAI_BASE_URL=http://%s/v1[-]\n", th.Green, a.p.Addr)
	fmt.Fprintf(&b, "  [%s]export OPENAI_API_KEY=any-value[-]\n\n", th.Green)

	return b.String()
}

func (a *App) updateStatusBar() {
	th := theme.Default

	entries := a.p.Store.List()
	inputTotal, outputTotal := a.p.Store.TotalTokens()

	var parts []string

	parts = append(parts, fmt.Sprintf("[%s::b] ⇆ %s[-::-]", th.Blue, a.p.Addr))
	parts = append(parts, fmt.Sprintf("[%s]%d requests[-]", th.Foreground, len(entries)))

	if inputTotal > 0 || outputTotal > 0 {
		parts = append(parts, fmt.Sprintf("[%s]%s in / %s out[-]",
			th.Cyan, tui.FormatTokens(int64(inputTotal)), tui.FormatTokens(int64(outputTotal))))
	}

	a.statusBar.SetText(strings.Join(parts, fmt.Sprintf(" [%s]•[-] ", th.BrBlack)))
}

func (a *App) renderTable() {
	th := theme.Default

	a.table.Clear()

	headers := []string{"Time", "Method", "Path", "Status", "Duration", "Model", "In", "Out"}
	for i, h := range headers {
		cell := tview.NewTableCell(fmt.Sprintf("[%s::b]%s[-::-]", th.BrBlack, h)).
			SetSelectable(false).
			SetExpansion(1)

		if i == 2 {
			cell.SetExpansion(3) // URL gets more space
		}

		a.table.SetCell(0, i, cell)
	}

	entries := a.p.Store.List()

	// Newest first
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		row := len(entries) - i

		statusColor := th.Green
		if e.Status >= 400 && e.Status < 500 {
			statusColor = th.Yellow
		} else if e.Status >= 500 {
			statusColor = th.Red
		} else if e.Status == 0 {
			statusColor = th.Red
		}

		statusText := fmt.Sprintf("%d", e.Status)
		if e.Status == 0 {
			statusText = "ERR"
		}

		dur := fmt.Sprintf("%dms", e.Duration.Milliseconds())
		if e.Duration >= time.Second {
			dur = fmt.Sprintf("%.1fs", e.Duration.Seconds())
		}

		cells := []struct {
			text  string
			color tcell.Color
		}{
			{e.Timestamp.Format("15:04:05"), th.BrBlack},
			{e.Method, th.Magenta},
			{requestURLPathText(e.URL), th.Foreground},
			{statusText, statusColor},
			{dur, th.BrBlack},
			{e.Model, th.Cyan},
			{tui.FormatTokens(int64(e.InputTokens)), th.BrBlack},
			{tui.FormatTokens(int64(e.OutputTokens)), th.BrBlack},
		}

		for col, c := range cells {
			cell := tview.NewTableCell(fmt.Sprintf("[%s]%s[-]", c.color, c.text)).
				SetExpansion(1)

			if col == 2 {
				cell.SetExpansion(3)
			}

			a.table.SetCell(row, col, cell)
		}
	}

	// Keep selection valid
	if rowCount := a.table.GetRowCount(); rowCount > 1 {
		row, _ := a.table.GetSelection()
		if row < 1 {
			a.table.Select(1, 0)
		}
	}
}

func (a *App) renderDetail() {
	th := theme.Default

	entry, ok := a.p.Store.Get(a.selectedID)
	if !ok {
		a.detail.SetText("[red]Request not found[-]")
		return
	}

	var b strings.Builder

	// Summary header
	statusColor := th.Green
	if entry.Status >= 400 {
		statusColor = th.Yellow
	}
	if entry.Status >= 500 || entry.Status == 0 {
		statusColor = th.Red
	}

	fmt.Fprintf(&b, "\n  [%s::b]Request Detail[-::-]\n\n", th.Blue)

	fmt.Fprintf(&b, "  [%s]Method[-]    [%s]%s[-]\n", th.BrBlack, th.Magenta, entry.Method)
	fmt.Fprintf(&b, "  [%s]URL[-]       [%s]%s[-]\n", th.BrBlack, th.Foreground, requestURLText(entry.URL))
	fmt.Fprintf(&b, "  [%s]Status[-]    [%s]%d[-]\n", th.BrBlack, statusColor, entry.Status)
	fmt.Fprintf(&b, "  [%s]Duration[-]  [%s]%s[-]\n", th.BrBlack, th.Foreground, entry.Duration.Round(time.Millisecond))
	fmt.Fprintf(&b, "  [%s]Model[-]     [%s]%s[-]\n", th.BrBlack, th.Cyan, entry.Model)

	if entry.InputTokens > 0 || entry.OutputTokens > 0 {
		if entry.CachedTokens > 0 {
			fmt.Fprintf(&b, "  [%s]Tokens[-]    [%s]%s in (%s cached) / %s out[-]\n",
				th.BrBlack, th.Cyan, tui.FormatTokens(int64(entry.InputTokens)), tui.FormatTokens(int64(entry.CachedTokens)), tui.FormatTokens(int64(entry.OutputTokens)))
		} else {
			fmt.Fprintf(&b, "  [%s]Tokens[-]    [%s]%s in / %s out[-]\n",
				th.BrBlack, th.Cyan, tui.FormatTokens(int64(entry.InputTokens)), tui.FormatTokens(int64(entry.OutputTokens)))
		}
	}

	if entry.Error != "" {
		fmt.Fprintf(&b, "  [%s]Error[-]     [%s]%s[-]\n", th.BrBlack, th.Red, entry.Error)
	}

	// Request body
	if len(entry.RequestBody) > 0 {
		fmt.Fprintf(&b, "\n  [%s::b]─── Request Body ───[-::-]\n\n", th.Yellow)
		fmt.Fprint(&b, formatJSON(entry.RequestBody, th))
	}

	// Response body
	if len(entry.ResponseBody) > 0 {
		fmt.Fprintf(&b, "\n  [%s::b]─── Response Body ───[-::-]\n\n", th.Yellow)

		if !looksLikeJSON(entry.ResponseBody) {
			fmt.Fprint(&b, formatSSEBody(entry.ResponseBody, th))
		} else {
			fmt.Fprint(&b, formatJSON(entry.ResponseBody, th))
		}
	}

	a.detail.SetText(b.String())
	a.detail.ScrollToBeginning()
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

func formatJSON(data []byte, th theme.Theme) string {
	var pretty bytes.Buffer

	if json.Indent(&pretty, data, "  ", "  ") == nil {
		return fmt.Sprintf("  [%s]%s[-]\n", th.Foreground, tview.Escape(pretty.String()))
	}

	return fmt.Sprintf("  [%s]%s[-]\n", th.Foreground, tview.Escape(string(data)))
}

func formatSSEBody(data []byte, th theme.Theme) string {
	var b strings.Builder

	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if after, ok := strings.CutPrefix(line, "data: "); ok {
			payload := after
			if payload == "[DONE]" {
				fmt.Fprintf(&b, "  [%s]data: [DONE][-]\n", th.BrBlack)
				continue
			}

			var pretty bytes.Buffer
			if json.Indent(&pretty, []byte(payload), "  ", "  ") == nil {
				fmt.Fprintf(&b, "  [%s]data: %s[-]\n", th.Foreground, tview.Escape(pretty.String()))
			} else {
				fmt.Fprintf(&b, "  [%s]%s[-]\n", th.Foreground, tview.Escape(line))
			}
		} else {
			fmt.Fprintf(&b, "  [%s]%s[-]\n", th.BrBlack, tview.Escape(line))
		}
	}

	return b.String()
}

func (a *App) saveEntry(entry proxy.RequestEntry) {
	name := fmt.Sprintf("%s.jsonl", entry.Timestamp.Format("20060102_150405"))

	if err := os.WriteFile(name, buildSavedEntry(entry), 0644); err != nil {
		a.statusBar.SetText(fmt.Sprintf("[red]Save failed: %v[-]", err))
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
