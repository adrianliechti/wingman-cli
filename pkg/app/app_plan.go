package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/plan"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

// planStats returns the formatted plan progress for the status bar (empty if no plan)
func (a *App) planStats() string {
	if a.plan.IsEmpty() {
		return ""
	}

	total := len(a.plan.Tasks)

	if total == 0 {
		return ""
	}

	var done int

	for _, task := range a.plan.Tasks {
		if task.Status == plan.StatusDone || task.Status == plan.StatusSkipped {
			done++
		}
	}

	t := theme.Default

	if done == total {
		return fmt.Sprintf("[%s]✓ %d/%d[-]", t.Green, done, total)
	}

	return fmt.Sprintf("[%s]◐ %d/%d[-]", t.Blue, done, total)
}

func (a *App) showPlanView() {
	t := theme.Default

	if a.plan.IsEmpty() {
		fmt.Fprintf(a.chatView, "[%s]No plan set[-]\n\n", t.Yellow)

		return
	}

	a.activeModal = ModalPlan

	total := len(a.plan.Tasks)

	var done int

	for _, task := range a.plan.Tasks {
		if task.Status == plan.StatusDone || task.Status == plan.StatusSkipped {
			done++
		}
	}

	// === PLAN VIEW ===
	planView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true)
	planView.SetBackgroundColor(tcell.ColorDefault)

	var sb strings.Builder

	// Title
	if a.plan.Title != "" {
		fmt.Fprintf(&sb, "[%s::b]%s[-::-]\n\n", t.Cyan, a.plan.Title)
	} else {
		fmt.Fprintf(&sb, "[%s::b]Plan[-::-]\n\n", t.Cyan)
	}

	// Tasks
	for i, task := range a.plan.Tasks {
		var statusColor tcell.Color
		var statusIcon string

		switch task.Status {
		case plan.StatusDone:
			statusColor = t.Green
			statusIcon = "✓"

		case plan.StatusRunning:
			statusColor = t.Blue
			statusIcon = "→"

		case plan.StatusSkipped:
			statusColor = t.BrBlack
			statusIcon = "○"

		default: // pending
			statusColor = t.Foreground
			statusIcon = " "
		}

		fmt.Fprintf(&sb, "  [#%06x]%s[-] [%s]%d.[-] %s\n",
			statusColor.Hex(), statusIcon, t.BrBlack, i+1, task.Description)
	}

	planView.SetText(sb.String())

	// === BOTTOM BAR ===
	hintBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	hintBar.SetBackgroundColor(tcell.ColorDefault)
	fmt.Fprintf(hintBar, "[%s]esc[-] [%s]close[-]  [%s]↑↓/jk[-] [%s]scroll[-]",
		t.BrBlack, t.Foreground, t.BrBlack, t.Foreground)

	statusBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignRight)
	statusBar.SetBackgroundColor(tcell.ColorDefault)

	if done == total {
		fmt.Fprintf(statusBar, "[%s]✓ %d/%d complete[-]", t.Green, done, total)
	} else {
		fmt.Fprintf(statusBar, "[%s]%d/%d complete[-]", t.Blue, done, total)
	}

	// Bottom bar with margins
	bottomBar := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(hintBar, 0, 1, false).
		AddItem(statusBar, 0, 1, false)
	bottomBar.SetBackgroundColor(tcell.ColorDefault)

	// Get margins based on compact mode
	leftMargin, rightMargin := a.getMargins()
	inputLeftMargin, inputRightMargin := a.getInputMargins()

	bottomBarWithMargins := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, inputLeftMargin, 0, false).
		AddItem(bottomBar, 0, 1, false).
		AddItem(nil, inputRightMargin, 0, false)
	bottomBarWithMargins.SetBackgroundColor(tcell.ColorDefault)

	// Content with margins
	contentWithMargins := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, leftMargin, 0, false).
		AddItem(planView, 0, 1, true).
		AddItem(nil, rightMargin, 0, false)
	contentWithMargins.SetBackgroundColor(tcell.ColorDefault)

	// Top spacer
	topSpacer := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)

	// Status spacer
	statusSpacer := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)

	// Final container
	container := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topSpacer, 1, 0, false).
		AddItem(contentWithMargins, 0, 1, true).
		AddItem(statusSpacer, 1, 0, false).
		AddItem(bottomBarWithMargins, 1, 0, false)
	container.SetBackgroundColor(tcell.ColorDefault)

	// === INPUT HANDLING ===
	planView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp:
			row, col := planView.GetScrollOffset()
			planView.ScrollTo(row-1, col)

			return nil

		case tcell.KeyDown:
			row, col := planView.GetScrollOffset()
			planView.ScrollTo(row+1, col)

			return nil
		}

		switch event.Rune() {
		case 'j':
			row, col := planView.GetScrollOffset()
			planView.ScrollTo(row+1, col)

			return nil

		case 'k':
			row, col := planView.GetScrollOffset()
			planView.ScrollTo(row-1, col)

			return nil
		}

		return event
	})

	if a.pages != nil {
		a.pages.AddPage("plan", container, true, true)
		a.app.SetFocus(planView)
	}
}

func (a *App) closePlanView() {
	a.activeModal = ModalNone

	if a.pages != nil {
		a.pages.RemovePage("plan")
		a.app.SetFocus(a.input)
	}
}