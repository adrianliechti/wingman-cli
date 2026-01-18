package app

import (
	"fmt"

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

	// Track selection state
	selectedIndex := 0

	// Create the file list view (custom rendering for clean look)
	fileList := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	fileList.SetBackgroundColor(tcell.ColorDefault)

	// Create the diff content view
	diffView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	diffView.SetBackgroundColor(tcell.ColorDefault)

	// Render file list with selection highlight
	renderFileList := func() {
		fileList.Clear()
		for i, diff := range diffs {
			var color tcell.Color
			var prefix string

			switch diff.Status {
			case rewind.StatusAdded:
				color = t.Green
				prefix = "+ "
			case rewind.StatusModified:
				color = t.Yellow
				prefix = "~ "
			case rewind.StatusDeleted:
				color = t.Red
				prefix = "- "
			default:
				color = t.Foreground
				prefix = "  "
			}

			// Highlight selected item with cyan, others with status color
			if i == selectedIndex {
				fmt.Fprintf(fileList, "[%s]▸ %s[-]\n", t.Cyan, diff.Path)
			} else {
				fmt.Fprintf(fileList, "[%s]%s%s[-]\n", color, prefix, diff.Path)
			}
		}
		// Scroll to keep selected item visible
		fileList.ScrollTo(selectedIndex, 0)
	}

	// Update diff view when selection changes
	updateDiffView := func() {
		if selectedIndex >= 0 && selectedIndex < len(diffs) {
			highlighted := markdown.HighlightDiff(diffs[selectedIndex].Patch)
			diffView.SetText(highlighted)
			diffView.ScrollToBeginning()
		}
	}

	// Initial render
	renderFileList()
	if len(diffs) > 0 {
		updateDiffView()
	}

	// Handle file list navigation
	fileList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp:
			if selectedIndex > 0 {
				selectedIndex--
				renderFileList()
				updateDiffView()
			}
			return nil
		case tcell.KeyDown:
			if selectedIndex < len(diffs)-1 {
				selectedIndex++
				renderFileList()
				updateDiffView()
			}
			return nil
		}
		return event
	})

	// Create a subtle separator
	separator := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)
	separator.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for i := y; i < y+height; i++ {
			screen.SetContent(x, i, '│', nil, tcell.StyleDefault.Foreground(t.BrBlack))
		}
		return x + 1, y, width - 1, height
	})

	// Layout: fileList on left, separator, diff on right
	content := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(fileList, 0, 2, true).
		AddItem(separator, 1, 0, false).
		AddItem(diffView, 0, 8, false)
	content.SetBackgroundColor(tcell.ColorDefault)

	// Bottom hint bar (like input hints)
	hintBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	hintBar.SetBackgroundColor(tcell.ColorDefault)
	fmt.Fprintf(hintBar, "[%s]esc[-] [%s]close[-]  [%s]tab[-] [%s]switch[-]  [%s]↑↓[-] [%s]select[-]  [%s]←→[-] [%s]scroll[-]", t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground)

	// Main container
	container := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(content, 0, 1, true).
		AddItem(hintBar, 1, 0, false)
	container.SetBackgroundColor(tcell.ColorDefault)

	// Track which panel has focus
	focusedPanel := 0 // 0 = fileList, 1 = diff

	// Handle input
	container.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			a.closeDiffView()
			return nil
		case tcell.KeyTab:
			// Toggle focus between panels
			if focusedPanel == 0 {
				focusedPanel = 1
				a.app.SetFocus(diffView)
			} else {
				focusedPanel = 0
				a.app.SetFocus(fileList)
			}
			return nil
		}
		return event
	})

	if a.pages != nil {
		a.pages.AddPage("diff", container, true, true)
		a.app.SetFocus(fileList)
	}
}

func (a *App) closeDiffView() {
	a.pickerActive = false
	if a.pages != nil {
		a.pages.RemovePage("diff")
		a.app.SetFocus(a.input)
	}
}
