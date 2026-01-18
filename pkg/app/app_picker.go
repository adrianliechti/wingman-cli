package app

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

type PickerItem struct {
	ID   string
	Text string
}

func (a *App) showPicker(title string, items []PickerItem, selectedID string, onSelect func(item PickerItem)) {
	if len(items) == 0 {
		return
	}

	a.pickerActive = true
	t := theme.Default

	list := tview.NewList().
		ShowSecondaryText(false)
	list.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)
	list.SetMainTextColor(t.Foreground)
	list.SetSelectedTextColor(t.Cyan)
	list.SetSelectedBackgroundColor(tview.Styles.PrimitiveBackgroundColor)

	currentIndex := 0
	for i, item := range items {
		if item.ID == selectedID {
			currentIndex = i
		}
		list.AddItem("  "+item.Text, "", 0, nil)
	}

	list.SetCurrentItem(currentIndex)

	list.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		a.closePicker()
		if onSelect != nil {
			onSelect(items[index])
		}
	})

	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyCtrlC {
			a.closePicker()
			return nil
		}
		return event
	})

	// Calculate dimensions
	maxWidth := len(title) + 4
	for _, item := range items {
		if len(item.Text)+6 > maxWidth {
			maxWidth = len(item.Text) + 6
		}
	}
	boxWidth := maxWidth + 4
	boxHeight := len(items) + 4

	// Create bordered container with opaque background
	box := tview.NewFlex().SetDirection(tview.FlexRow)
	box.Box = tview.NewBox()
	box.AddItem(list, 0, 1, true)
	box.SetBorder(true)
	box.SetBorderColor(t.Cyan)
	box.SetTitle(" " + title + " ")
	box.SetTitleColor(t.Cyan)
	box.SetTitleAlign(tview.AlignCenter)
	box.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)
	box.SetBorderPadding(1, 1, 2, 2)

	// Create centered modal layout
	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(box, boxHeight, 0, true).
			AddItem(nil, 0, 1, false), boxWidth, 0, true).
		AddItem(nil, 0, 1, false)

	modal.SetBackgroundColor(tcell.ColorDefault)

	if a.pages != nil {
		a.pages.AddPage("picker", modal, true, true)
		a.app.SetFocus(list)
	}
}

func (a *App) closePicker() {
	a.pickerActive = false
	if a.pages != nil {
		a.pages.RemovePage("picker")
		a.app.SetFocus(a.input)
	}
}
