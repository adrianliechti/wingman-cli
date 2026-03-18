package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/theme"
	"github.com/adrianliechti/wingman-agent/pkg/ui"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	pageStart  = "start"
	pageList   = "list"
	pageDetail = "detail"
)


type tui struct {
	app   *tview.Application
	pages *tview.Pages
	store *Store

	statusBar *tview.TextView
	table     *tview.Table
	detail    *tview.TextView
	startView *tview.TextView

	listenAddr string
	upstream   string

	selectedID string
	activePage string

	seenRequests int
}

func newTUI(store *Store, listenAddr, upstream string) *tui {
	t := &tui{
		app:        tview.NewApplication(),
		store:      store,
		listenAddr: listenAddr,
		upstream:   upstream,
		activePage: pageStart,
	}

	t.build()
	return t
}

func (t *tui) build() {
	th := theme.Default

	t.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		screen.Clear()
		return false
	})

	// Status bar
	t.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	t.statusBar.SetBackgroundColor(tcell.ColorDefault)
	t.updateStatusBar()

	// Start page
	t.startView = tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true)
	t.startView.SetBackgroundColor(tcell.ColorDefault)
	t.startView.SetText(t.startPageContent())

	// Request list table
	t.table = tview.NewTable().
		SetSelectable(true, false).
		SetFixed(1, 0)
	t.table.SetBackgroundColor(tcell.ColorDefault)
	t.table.SetSelectedStyle(tcell.StyleDefault.
		Background(th.Selection).
		Foreground(th.Foreground))
	t.renderTable()

	// Detail view
	t.detail = tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(true)
	t.detail.SetBackgroundColor(tcell.ColorDefault)

	// Pages
	t.pages = tview.NewPages()
	t.pages.SetBackgroundColor(tcell.ColorDefault)

	startLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	startLayout.SetBackgroundColor(tcell.ColorDefault)
	startLayout.AddItem(t.startView, 0, 1, true)

	listLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	listLayout.SetBackgroundColor(tcell.ColorDefault)
	listLayout.AddItem(t.table, 0, 1, true)
	listLayout.AddItem(t.statusBar, 1, 0, false)

	detailLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	detailLayout.SetBackgroundColor(tcell.ColorDefault)
	detailLayout.AddItem(t.detail, 0, 1, true)
	detailLayout.AddItem(t.statusBar, 1, 0, false)

	t.pages.AddPage(pageStart, startLayout, true, true)
	t.pages.AddPage(pageList, listLayout, true, false)
	t.pages.AddPage(pageDetail, detailLayout, true, false)

	// Input handling
	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch t.activePage {
		case pageStart:
			switch event.Key() {
			case tcell.KeyEnter:
				t.switchTo(pageList)
				return nil
			case tcell.KeyEscape, tcell.KeyCtrlC:
				t.app.Stop()
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'l' || event.Rune() == 'q' {
					if event.Rune() == 'q' {
						t.app.Stop()
						return nil
					}
					t.switchTo(pageList)
					return nil
				}
			}

		case pageList:
			switch event.Key() {
			case tcell.KeyEnter:
				row, _ := t.table.GetSelection()
				if row > 0 {
					entries := t.store.List()
					idx := len(entries) - row // newest first, row 0 is header
					if idx >= 0 && idx < len(entries) {
						t.selectedID = entries[idx].ID
						t.renderDetail()
						t.switchTo(pageDetail)
					}
				}
				return nil
			case tcell.KeyCtrlC:
				t.app.Stop()
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'q' {
					t.app.Stop()
					return nil
				}
			}

		case pageDetail:
			switch event.Key() {
			case tcell.KeyEscape:
				t.switchTo(pageList)
				return nil
			case tcell.KeyCtrlC:
				t.app.Stop()
				return nil
			case tcell.KeyRune:
				if event.Rune() == 'q' {
					t.app.Stop()
					return nil
				}
				if event.Rune() == 's' {
					if entry, ok := t.store.Get(t.selectedID); ok {
						t.saveEntry(entry)
					}
					return nil
				}
				if event.Rune() == ' ' {
					row, col := t.detail.GetScrollOffset()
					_, _, _, height := t.detail.GetInnerRect()
					t.detail.ScrollTo(row+height, col)
					return nil
				}
			}
		}

		return event
	})

	t.app.SetRoot(t.pages, true)
}

func (t *tui) switchTo(page string) {
	t.activePage = page
	t.pages.SwitchToPage(page)

	switch page {
	case pageStart:
		t.app.SetFocus(t.startView)
	case pageList:
		t.renderTable()
		t.app.SetFocus(t.table)
	case pageDetail:
		t.app.SetFocus(t.detail)
	}
}

func (t *tui) Run() error {
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			t.app.QueueUpdateDraw(func() {
				entries := t.store.List()

				// Auto-switch to list on first request
				if t.activePage == pageStart && len(entries) > 0 && t.seenRequests == 0 {
					t.seenRequests = len(entries)
					t.switchTo(pageList)
				} else {
					t.seenRequests = len(entries)
				}

				if t.activePage == pageList {
					t.renderTable()
				}

				t.updateStatusBar()
			})
		}
	}()

	return t.app.Run()
}

func (t *tui) Stop() {
	t.app.Stop()
}

func (t *tui) startPageContent() string {
	th := theme.Default

	var b strings.Builder

	fmt.Fprint(&b, strings.ReplaceAll(ui.Logo, "\n", "\n  "))
	fmt.Fprint(&b, "\n")

	fmt.Fprintf(&b, "  [%s::b]Listening[-::-]  [%s]http://%s[-]\n\n", th.Yellow, th.Foreground, t.listenAddr)

	fmt.Fprintf(&b, "  [%s::b]Usage[-::-]\n", th.Cyan)
	fmt.Fprintf(&b, "  [%s]Point your OpenAI client to the proxy:[-]\n\n", th.BrBlack)
	fmt.Fprintf(&b, "  [%s]export OPENAI_BASE_URL=http://%s/v1[-]\n", th.Green, t.listenAddr)
	fmt.Fprintf(&b, "  [%s]export OPENAI_API_KEY=any-value[-]\n\n", th.Green)

	return b.String()
}

func (t *tui) updateStatusBar() {
	th := theme.Default

	entries := t.store.List()
	inputTotal, outputTotal := t.store.TotalTokens()

	var parts []string

	parts = append(parts, fmt.Sprintf("[%s::b] ⇆ %s[-::-]", th.Blue, t.listenAddr))
	parts = append(parts, fmt.Sprintf("[%s]%d requests[-]", th.Foreground, len(entries)))

	if inputTotal > 0 || outputTotal > 0 {
		parts = append(parts, fmt.Sprintf("[%s]%s in / %s out[-]",
			th.Cyan, formatTokenCount(inputTotal), formatTokenCount(outputTotal)))
	}

	t.statusBar.SetText(strings.Join(parts, fmt.Sprintf(" [%s]•[-] ", th.BrBlack)))
}

func (t *tui) renderTable() {
	th := theme.Default

	t.table.Clear()

	headers := []string{"Time", "Method", "Path", "Status", "Duration", "Model", "In", "Out"}
	for i, h := range headers {
		cell := tview.NewTableCell(fmt.Sprintf("[%s::b]%s[-::-]", th.BrBlack, h)).
			SetSelectable(false).
			SetExpansion(1)

		if i == 2 {
			cell.SetExpansion(3) // URL gets more space
		}

		t.table.SetCell(0, i, cell)
	}

	entries := t.store.List()

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
			{formatTokenCount(e.InputTokens), th.BrBlack},
			{formatTokenCount(e.OutputTokens), th.BrBlack},
		}

		for col, c := range cells {
			cell := tview.NewTableCell(fmt.Sprintf("[%s]%s[-]", c.color, c.text)).
				SetExpansion(1)

			if col == 2 {
				cell.SetExpansion(3)
			}

			t.table.SetCell(row, col, cell)
		}
	}

	// Keep selection valid
	if rowCount := t.table.GetRowCount(); rowCount > 1 {
		row, _ := t.table.GetSelection()
		if row < 1 {
			t.table.Select(1, 0)
		}
	}
}

func (t *tui) renderDetail() {
	th := theme.Default

	entry, ok := t.store.Get(t.selectedID)
	if !ok {
		t.detail.SetText("[red]Request not found[-]")
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
		fmt.Fprintf(&b, "  [%s]Tokens[-]    [%s]%s in / %s out[-]\n",
			th.BrBlack, th.Cyan, formatTokenCount(entry.InputTokens), formatTokenCount(entry.OutputTokens))
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

		if !isJSON(entry.ResponseBody) {
			fmt.Fprint(&b, formatSSEBody(entry.ResponseBody, th))
		} else {
			fmt.Fprint(&b, formatJSON(entry.ResponseBody, th))
		}
	}

	t.detail.SetText(b.String())
	t.detail.ScrollToBeginning()
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

func (t *tui) saveEntry(entry RequestEntry) {
	name := fmt.Sprintf("%s.jsonl", entry.Timestamp.Format("20060102_150405"))

	if err := os.WriteFile(name, buildSavedEntry(entry), 0644); err != nil {
		t.statusBar.SetText(fmt.Sprintf("[red]Save failed: %v[-]", err))
	}
}

func buildSavedEntry(entry RequestEntry) []byte {
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

func formatTokenCount(n int) string {
	if n == 0 {
		return "0"
	}

	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}

	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}

	return fmt.Sprintf("%d", n)
}
