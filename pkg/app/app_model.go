package app

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func (a *App) showModelPicker() {
	a.modelPickerActive = true
	t := theme.Default

	list := tview.NewList().
		ShowSecondaryText(false)
	list.SetBorder(true)
	list.SetTitle(" Select Model ")
	list.SetTitleAlign(tview.AlignCenter)
	list.SetBorderColor(t.Cyan)
	list.SetBackgroundColor(t.Background)
	list.SetMainTextColor(t.Foreground)
	list.SetSelectedTextColor(t.Background)
	list.SetSelectedBackgroundColor(t.Cyan)

	currentIndex := 0
	for i, model := range config.AvailableModels {
		if model == a.config.Model {
			currentIndex = i
		}
		list.AddItem(model, "", 0, nil)
	}

	list.SetCurrentItem(currentIndex)

	list.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		a.config.Model = config.AvailableModels[index]
		a.updateStatusBar()
		a.closeModelPicker()
	})

	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			a.closeModelPicker()
			return nil
		}
		return event
	})

	// Create a centered modal layout
	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(list, len(config.AvailableModels)+2, 0, true).
			AddItem(nil, 0, 1, false), 30, 0, true).
		AddItem(nil, 0, 1, false)

	modal.SetBackgroundColor(tcell.ColorDefault)

	a.app.SetRoot(modal, true).SetFocus(list)
}

func (a *App) closeModelPicker() {
	a.modelPickerActive = false
	a.app.SetRoot(a.buildLayout(), true).SetFocus(a.input)
}
