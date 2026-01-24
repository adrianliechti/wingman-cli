package app

import (
	"fmt"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func (a *App) showRewindPicker() {
	if a.rewind == nil {
		t := theme.Default
		fmt.Fprintf(a.chatView, "[%s]Rewind not available[-]\n\n", t.Yellow)

		return
	}

	checkpoints, err := a.rewind.List()

	if err != nil || len(checkpoints) == 0 {
		t := theme.Default
		fmt.Fprintf(a.chatView, "[%s]No checkpoints available[-]\n\n", t.Yellow)

		return
	}

	items := make([]PickerItem, len(checkpoints))

	for i, cp := range checkpoints {
		items[i] = PickerItem{
			ID:   cp.Hash,
			Text: fmt.Sprintf("%s - %s", cp.Time.Format("15:04:05"), cp.Message),
		}
	}

	a.showPicker("Rewind to", items, "", func(item PickerItem) {
		if err := a.rewind.Restore(item.ID); err != nil {
			t := theme.Default
			fmt.Fprintf(a.chatView, "[%s]Failed to restore: %v[-]\n\n", t.Red, err)

			return
		}

		t := theme.Default
		fmt.Fprintf(a.chatView, "[%s]Restored to: %s[-]\n\n", t.Green, item.Text)
	})
}

func (a *App) commitRewind(message string) {
	if a.rewind == nil {
		return
	}

	if len(message) > 50 {
		message = message[:50]
	}

	go func() {
		_ = a.rewind.Commit(message)
	}()
}