package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	pageStart  = "start"
	pageList   = "list"
	pageDetail = "detail"
)

const logo = `
[#84a0c6]██╗    ██╗[#89b8c2]██╗[#b4be82]███╗   ██╗[#e2a478] ██████╗[#e27878]███╗   ███╗[#a093c7] █████╗ [#91acd1]███╗   ██╗
[#84a0c6]██║    ██║[#89b8c2]██║[#b4be82]████╗  ██║[#e2a478]██╔════╝[#e27878]████╗ ████║[#a093c7]██╔══██╗[#91acd1]████╗  ██║
[#84a0c6]██║ █╗ ██║[#89b8c2]██║[#b4be82]██╔██╗ ██║[#e2a478]██║  ███╗[#e27878]██╔████╔██║[#a093c7]███████║[#91acd1]██╔██╗ ██║
[#84a0c6]██║███╗██║[#89b8c2]██║[#b4be82]██║╚██╗██║[#e2a478]██║   ██║[#e27878]██║╚██╔╝██║[#a093c7]██╔══██║[#91acd1]██║╚██╗██║
[#84a0c6]╚███╔███╔╝[#89b8c2]██║[#b4be82]██║ ╚████║[#e2a478]╚██████╔╝[#e27878]██║ ╚═╝ ██║[#a093c7]██║  ██║[#91acd1]██║ ╚████║
[#84a0c6] ╚══╝╚══╝ [#89b8c2]╚═╝[#b4be82]╚═╝  ╚═══╝[#e2a478] ╚═════╝ [#e27878]╚═╝     ╚═╝[#a093c7]╚═╝  ╚═╝[#91acd1]╚═╝  ╚═══╝[-]
`

type tui struct {
	app   *tview.Application
	pages *tview.Pages
	store *Store

	statusBar *tview.TextView
	table     *tview.Table
	detail    *tview.TextView
	startView *tview.TextView

	listenAddr string
	tools      []tool.Tool

	selectedID int
	activePage string

	seenEntries int
}

func newTUI(store *Store, listenAddr string, tools []tool.Tool) *tui {
	t := &tui{
		app:        tview.NewApplication(),
		store:      store,
		listenAddr: listenAddr,
		tools:      tools,
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

	// Tool call list table
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
					idx := len(entries) - row
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
		for range t.store.Notify() {
			t.app.QueueUpdateDraw(func() {
				entries := t.store.List()

				if t.activePage == pageStart && len(entries) > 0 && t.seenEntries == 0 {
					t.seenEntries = len(entries)
					t.switchTo(pageList)
				} else {
					t.seenEntries = len(entries)
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

func (t *tui) startPageContent() string {
	th := theme.Default

	var b strings.Builder

	b.WriteString(strings.ReplaceAll(logo, "\n", "\n  "))
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("  [%s::b]MCP Server[-::-]  [%s]http://%s/mcp[-]\n\n", th.Yellow, th.Foreground, t.listenAddr))

	b.WriteString(fmt.Sprintf("  [%s::b]Tools[-::-] [%s](%d available)[-]\n", th.Cyan, th.BrBlack, len(t.tools)))

	for _, tool := range t.tools {
		if tool.Hidden {
			continue
		}

		desc := tool.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}

		b.WriteString(fmt.Sprintf("  [%s]%-20s[%s] %s[-]\n", th.Green, tool.Name, th.BrBlack, desc))
	}

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  [%s]Press Enter or 'l' to view tool calls, 'q' to quit[-]\n", th.BrBlack))

	return b.String()
}

func (t *tui) updateStatusBar() {
	th := theme.Default

	entries := t.store.List()

	var running int
	for _, e := range entries {
		if e.Status == "running" {
			running++
		}
	}

	var parts []string

	parts = append(parts, fmt.Sprintf("[%s::b] MCP %s[-::-]", th.Blue, t.listenAddr))
	parts = append(parts, fmt.Sprintf("[%s]%d calls[-]", th.Foreground, len(entries)))

	if running > 0 {
		parts = append(parts, fmt.Sprintf("[%s]%d running[-]", th.Yellow, running))
	}

	t.statusBar.SetText(strings.Join(parts, fmt.Sprintf(" [%s]•[-] ", th.BrBlack)))
}

func (t *tui) renderTable() {
	th := theme.Default

	t.table.Clear()

	headers := []string{"Time", "Tool", "Status", "Duration", "Arguments"}
	for i, h := range headers {
		cell := tview.NewTableCell(fmt.Sprintf("[%s::b]%s[-::-]", th.BrBlack, h)).
			SetSelectable(false).
			SetExpansion(1)

		if i == 1 {
			cell.SetExpansion(2)
		}
		if i == 4 {
			cell.SetExpansion(4)
		}

		t.table.SetCell(0, i, cell)
	}

	entries := t.store.List()

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		row := len(entries) - i

		statusColor := th.Yellow
		statusText := "running"

		switch e.Status {
		case "done":
			statusColor = th.Green
			statusText = "done"
		case "error":
			statusColor = th.Red
			statusText = "error"
		}

		dur := ""
		if e.Status != "running" {
			dur = fmt.Sprintf("%dms", e.Duration.Milliseconds())
			if e.Duration >= time.Second {
				dur = fmt.Sprintf("%.1fs", e.Duration.Seconds())
			}
		}

		args := e.Arguments
		if len(args) > 80 {
			args = args[:77] + "..."
		}

		cells := []struct {
			text  string
			color tcell.Color
		}{
			{e.Timestamp.Format("15:04:05"), th.BrBlack},
			{e.Tool, th.Cyan},
			{statusText, statusColor},
			{dur, th.BrBlack},
			{args, th.Foreground},
		}

		for col, c := range cells {
			cell := tview.NewTableCell(fmt.Sprintf("[%s]%s[-]", c.color, tview.Escape(c.text))).
				SetExpansion(1)

			if col == 1 {
				cell.SetExpansion(2)
			}
			if col == 4 {
				cell.SetExpansion(4)
			}

			t.table.SetCell(row, col, cell)
		}
	}

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
		t.detail.SetText("[red]Entry not found[-]")
		return
	}

	var b strings.Builder

	statusColor := th.Yellow
	switch entry.Status {
	case "done":
		statusColor = th.Green
	case "error":
		statusColor = th.Red
	}

	b.WriteString(fmt.Sprintf("\n  [%s::b]Tool Call Detail[-::-]\n\n", th.Blue))

	b.WriteString(fmt.Sprintf("  [%s]Tool[-]      [%s]%s[-]\n", th.BrBlack, th.Cyan, entry.Tool))
	b.WriteString(fmt.Sprintf("  [%s]Status[-]    [%s]%s[-]\n", th.BrBlack, statusColor, entry.Status))
	b.WriteString(fmt.Sprintf("  [%s]Time[-]      [%s]%s[-]\n", th.BrBlack, th.Foreground, entry.Timestamp.Format("15:04:05")))

	if entry.Status != "running" {
		b.WriteString(fmt.Sprintf("  [%s]Duration[-]  [%s]%s[-]\n", th.BrBlack, th.Foreground, entry.Duration.Round(time.Millisecond)))
	}

	if entry.Error != "" {
		b.WriteString(fmt.Sprintf("  [%s]Error[-]     [%s]%s[-]\n", th.BrBlack, th.Red, tview.Escape(entry.Error)))
	}

	if entry.Arguments != "" && entry.Arguments != "{}" {
		b.WriteString(fmt.Sprintf("\n  [%s::b]─── Arguments ───[-::-]\n\n", th.Yellow))
		b.WriteString(fmt.Sprintf("  [%s]%s[-]\n", th.Foreground, tview.Escape(entry.Arguments)))
	}

	if entry.Result != "" {
		b.WriteString(fmt.Sprintf("\n  [%s::b]─── Result ───[-::-]\n\n", th.Yellow))

		result := entry.Result
		if len(result) > 5000 {
			result = result[:5000] + "\n... (truncated)"
		}

		b.WriteString(fmt.Sprintf("  [%s]%s[-]\n", th.Foreground, tview.Escape(result)))
	}

	t.detail.SetText(b.String())
	t.detail.ScrollToBeginning()
}
