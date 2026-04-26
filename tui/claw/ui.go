package claw

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (t *TUI) buildUI() {
	th := theme.Default
	t.app = tview.NewApplication()

	// Agent list
	t.agentList = tview.NewList()
	t.agentList.SetBorder(false)
	t.agentList.SetHighlightFullLine(true)
	t.agentList.ShowSecondaryText(false)
	t.agentList.SetMainTextColor(th.Foreground)
	t.agentList.SetSelectedTextColor(th.Cyan)
	t.agentList.SetSelectedBackgroundColor(th.Selection)
	t.agentList.SetSelectedFunc(func(index int, name string, _ string, _ rune) {
		t.selectAgent(strings.TrimSpace(name))
		t.app.SetFocus(t.input)
	})

	// Task view
	t.taskView = tview.NewTextView()
	t.taskView.SetBorder(false)
	t.taskView.SetDynamicColors(true)
	t.taskView.SetWordWrap(true)

	// Chat view
	t.chatView = tview.NewTextView()
	t.chatView.SetBorder(false)
	t.chatView.SetDynamicColors(true)
	t.chatView.SetWordWrap(false)
	t.chatView.SetScrollable(true)
	t.chatView.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		t.chatWidth = width
		return x, y, width, height
	})
	t.chatView.SetChangedFunc(func() {
		t.app.Draw()
	})

	// Input
	t.input = tview.NewInputField()
	t.input.SetLabel("  \u276f ")
	t.input.SetLabelColor(th.Cyan)
	t.input.SetFieldBackgroundColor(th.Selection)
	t.input.SetFieldTextColor(th.Foreground)
	t.input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			t.submitInput()
		}
	})

	// Status bar
	t.statusBar = tview.NewTextView()
	t.statusBar.SetDynamicColors(true)
	t.statusBar.SetTextAlign(tview.AlignRight)

	// Sidebar titles
	sidebarTitle := tview.NewTextView()
	sidebarTitle.SetDynamicColors(true)
	sidebarTitle.SetText(fmt.Sprintf("\n  [%s::b]Agents[-::-]\n", th.Cyan))

	taskTitle := tview.NewTextView()
	taskTitle.SetDynamicColors(true)
	taskTitle.SetText(fmt.Sprintf("\n  [%s::b]Tasks[-::-]\n", th.Yellow))

	// Sidebar
	sidebar := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(sidebarTitle, 3, 0, false).
		AddItem(t.agentList, 0, 1, true)

	// Vertical separator
	vSep := tview.NewBox().SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			screen.SetContent(x, row, '\u2502', nil, tcell.StyleDefault.Foreground(th.BrBlack))
		}
		return x + 1, y, width - 1, height
	})

	// Horizontal separator
	hSep := tview.NewBox().SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for col := x; col < x+width; col++ {
			screen.SetContent(col, y, '\u2500', nil, tcell.StyleDefault.Foreground(th.BrBlack))
		}
		return x, y + 1, width, height - 1
	})

	// Bottom bar
	bottom := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.input, 1, 0, false).
		AddItem(t.statusBar, 1, 0, false)

	// Task panel (title + scrollable tasks)
	taskPanel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(taskTitle, 3, 0, false).
		AddItem(t.taskView, 0, 1, false)

	// Right side: tasks (1/3) + separator + chat (2/3) + bottom
	rightSide := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(taskPanel, 0, 1, false).
		AddItem(hSep, 1, 0, false).
		AddItem(t.chatView, 0, 2, false).
		AddItem(bottom, 2, 0, false)

	// Root
	root := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(sidebar, 24, 0, true).
		AddItem(vSep, 1, 0, false).
		AddItem(rightSide, 0, 1, false)

	t.app.SetRoot(root, true)
	t.app.SetFocus(t.input)
	t.app.EnableMouse(true)

	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			t.cycleFocus()
			return nil
		case tcell.KeyCtrlC:
			t.app.Stop()
			return nil
		}

		return event
	})
}
