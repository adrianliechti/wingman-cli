package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/markdown"
	"github.com/adrianliechti/wingman-cli/pkg/rewind"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func (a *App) showDiffView() {
	if a.rewind == nil {
		t := theme.Default
		fmt.Fprintf(a.chatView, "[%s]Diff not available (rewind not initialized)[-]\n\n", t.Yellow)
		return
	}

	diffs, err := a.rewind.DiffFromBaseline()
	if err != nil {
		t := theme.Default
		fmt.Fprintf(a.chatView, "[%s]%v[-]\n\n", t.Yellow, err)
		return
	}

	a.pickerActive = true
	t := theme.Default

	// Calculate stats
	var added, modified, deleted int
	var totalInsertions, totalDeletions int
	for _, diff := range diffs {
		switch diff.Status {
		case rewind.StatusAdded:
			added++
		case rewind.StatusModified:
			modified++
		case rewind.StatusDeleted:
			deleted++
		}
		ins, del := countDiffStats(diff.Patch)
		totalInsertions += ins
		totalDeletions += del
	}

	// Track selection state
	selectedIndex := 0

	// === FILE LIST ===
	fileListView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	fileListView.SetBackgroundColor(tcell.ColorDefault)

	renderFileList := func() {
		fileListView.Clear()
		var sb strings.Builder

		for i, diff := range diffs {
			var statusColor tcell.Color
			var statusIcon string

			switch diff.Status {
			case rewind.StatusAdded:
				statusColor = t.Green
				statusIcon = "●"
			case rewind.StatusModified:
				statusColor = t.Yellow
				statusIcon = "●"
			case rewind.StatusDeleted:
				statusColor = t.Red
				statusIcon = "●"
			default:
				statusColor = t.Foreground
				statusIcon = "○"
			}

			// Get per-file stats
			ins, del := countDiffStats(diff.Patch)
			statsStr := fmt.Sprintf("[%s]+%d[-] [%s]-%d[-]", t.Green, ins, t.Red, del)

			if i == selectedIndex {
				// Selected: cyan arrow, cyan filename
				fmt.Fprintf(&sb, "  [%s]▶[-] [%s]%s[-] [%s::b]%s[-::-] %s\n",
					t.Cyan, statusColor, statusIcon, t.Cyan, diff.Path, statsStr)
			} else {
				// Unselected: status colored icon, normal filename
				fmt.Fprintf(&sb, "    [%s]%s[-] [%s]%s[-] %s\n",
					statusColor, statusIcon, t.Foreground, diff.Path, statsStr)
			}
		}

		fileListView.SetText(sb.String())
		fileListView.ScrollTo(selectedIndex, 0)
	}

	// === DIFF CONTENT ===
	diffContentView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	diffContentView.SetBackgroundColor(tcell.ColorDefault)

	renderDiffContent := func() {
		if selectedIndex < 0 || selectedIndex >= len(diffs) {
			return
		}

		diff := diffs[selectedIndex]
		diffContentView.Clear()

		// Diff content with syntax highlighting (no header - path is already in the diff)
		highlighted := markdown.HighlightDiff(diff.Patch)
		diffContentView.SetText(highlighted)
		diffContentView.ScrollToBeginning()
	}

	// Initial render
	renderFileList()
	if len(diffs) > 0 {
		renderDiffContent()
	}

	// === BOTTOM BAR (hint + status) ===
	hintBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	hintBar.SetBackgroundColor(tcell.ColorDefault)

	statusBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignRight)
	statusBar.SetBackgroundColor(tcell.ColorDefault)

	// Build status text
	var statParts []string
	if added > 0 {
		statParts = append(statParts, fmt.Sprintf("[%s]+%d[-]", t.Green, added))
	}
	if modified > 0 {
		statParts = append(statParts, fmt.Sprintf("[%s]~%d[-]", t.Yellow, modified))
	}
	if deleted > 0 {
		statParts = append(statParts, fmt.Sprintf("[%s]-%d[-]", t.Red, deleted))
	}
	fmt.Fprintf(statusBar, "[%s]%d file(s)[-] %s  [%s]+%d[-] [%s]-%d[-]",
		t.BrBlack, len(diffs), strings.Join(statParts, " "), t.Green, totalInsertions, t.Red, totalDeletions)

	// Track which panel has focus
	focusedPanel := 0 // 0 = fileList, 1 = diffContent

	updateHintBar := func() {
		hintBar.Clear()
		if focusedPanel == 0 {
			fmt.Fprintf(hintBar, "[%s]esc[-] [%s]close[-]  [%s]tab[-] [%s]switch[-]  [%s]↑↓/jk[-] [%s]select[-]",
				t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground)
		} else {
			fmt.Fprintf(hintBar, "[%s]esc[-] [%s]close[-]  [%s]tab[-] [%s]switch[-]  [%s]↑↓/jk[-] [%s]scroll[-]  [%s]g/G[-] [%s]top/bottom[-]",
				t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground)
		}
	}
	updateHintBar()

	// === LAYOUT ===

	// Vertical separator between panels
	separator := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)
	separator.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		sepColor := t.BrBlack
		for i := y; i < y+height; i++ {
			screen.SetContent(x, i, '│', nil, tcell.StyleDefault.Foreground(sepColor))
		}
		return x + 1, y, width - 1, height
	})

	// Two-pane content area
	panelsContainer := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(fileListView, 40, 0, true).
		AddItem(separator, 1, 0, false).
		AddItem(diffContentView, 0, 1, false)
	panelsContainer.SetBackgroundColor(tcell.ColorDefault)

	// Add margins (matching chat mode: 2 left, 4 right)
	contentWithMargins := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, 2, 0, false).
		AddItem(panelsContainer, 0, 1, true).
		AddItem(nil, 4, 0, false)
	contentWithMargins.SetBackgroundColor(tcell.ColorDefault)

	// Bottom bar with hint and status
	bottomBar := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(hintBar, 0, 1, false).
		AddItem(statusBar, 0, 1, false)
	bottomBar.SetBackgroundColor(tcell.ColorDefault)
	// Clear the entire bottom bar area before drawing to prevent underlying content from showing through
	bottomBar.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			for col := x; col < x+width; col++ {
				screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
			}
		}
		return x, y, width, height
	})

	// Bottom bar with margins
	bottomBarWithMargins := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, 4, 0, false).
		AddItem(bottomBar, 0, 1, false).
		AddItem(nil, 4, 0, false)
	bottomBarWithMargins.SetBackgroundColor(tcell.ColorDefault)
	// Clear margins as well
	bottomBarWithMargins.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			for col := x; col < x+width; col++ {
				screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
			}
		}
		return x, y, width, height
	})

	// Top spacer - blank line
	topSpacer := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)

	// Spacer above status bar - blank line
	statusSpacer := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)
	statusSpacer.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			for col := x; col < x+width; col++ {
				screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
			}
		}
		return x, y, width, height
	})

	// Final container
	container := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topSpacer, 1, 0, false).
		AddItem(contentWithMargins, 0, 1, true).
		AddItem(statusSpacer, 1, 0, false).
		AddItem(bottomBarWithMargins, 1, 0, false)
	container.SetBackgroundColor(tcell.ColorDefault)

	// === INPUT HANDLING ===

	fileListView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp:
			if selectedIndex > 0 {
				selectedIndex--
				renderFileList()
				renderDiffContent()
			}
			return nil
		case tcell.KeyDown:
			if selectedIndex < len(diffs)-1 {
				selectedIndex++
				renderFileList()
				renderDiffContent()
			}
			return nil
		}
		switch event.Rune() {
		case 'k':
			if selectedIndex > 0 {
				selectedIndex--
				renderFileList()
				renderDiffContent()
			}
			return nil
		case 'j':
			if selectedIndex < len(diffs)-1 {
				selectedIndex++
				renderFileList()
				renderDiffContent()
			}
			return nil
		}
		return event
	})

	diffContentView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, col := diffContentView.GetScrollOffset()
		switch event.Key() {
		case tcell.KeyUp:
			if row > 0 {
				diffContentView.ScrollTo(row-1, col)
			}
			return nil
		case tcell.KeyDown:
			diffContentView.ScrollTo(row+1, col)
			return nil
		}
		switch event.Rune() {
		case 'j':
			diffContentView.ScrollTo(row+1, col)
			return nil
		case 'k':
			if row > 0 {
				diffContentView.ScrollTo(row-1, col)
			}
			return nil
		case 'g':
			diffContentView.ScrollToBeginning()
			return nil
		case 'G':
			diffContentView.ScrollToEnd()
			return nil
		}
		return event
	})

	container.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			a.closeDiffView()
			return nil
		case tcell.KeyTab:
			if focusedPanel == 0 {
				focusedPanel = 1
				a.app.SetFocus(diffContentView)
				updateHintBar()
			} else {
				focusedPanel = 0
				a.app.SetFocus(fileListView)
				updateHintBar()
			}
			return nil
		}
		return event
	})

	if a.pages != nil {
		a.pages.AddPage("diff", container, true, true)
		a.app.SetFocus(fileListView)
	}
}

func (a *App) closeDiffView() {
	a.pickerActive = false
	if a.pages != nil {
		a.pages.RemovePage("diff")
		a.app.SetFocus(a.input)
	}
}

// countDiffStats counts insertions and deletions in a unified diff patch
func countDiffStats(patch string) (insertions, deletions int) {
	for _, line := range strings.Split(patch, "\n") {
		if len(line) == 0 {
			continue
		}
		// Skip diff headers
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "diff ") ||
			strings.HasPrefix(line, "index ") {
			continue
		}
		if line[0] == '+' {
			insertions++
		} else if line[0] == '-' {
			deletions++
		}
	}
	return
}
