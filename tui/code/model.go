package code

import (
	"slices"

	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func (a *App) autoSelectModel() {
	if a.agent.Model != nil && a.agent.Model() != "" {
		return
	}

	models, err := a.agent.Models(a.ctx)

	if err != nil {
		return
	}

	for _, allowed := range code.AvailableModels {
		for _, m := range models {
			if m.ID == allowed.ID {
				a.setModel(m.ID)

				return
			}
		}
	}

	if len(models) > 0 {
		a.setModel(models[0].ID)
	}
}

func (a *App) showModelPicker() {
	models, err := a.agent.Models(a.ctx)

	if err != nil {
		return
	}

	var items []PickerItem

	for _, allowed := range code.AvailableModels {
		for _, m := range models {
			if m.ID == allowed.ID {
				items = append(items, PickerItem{ID: m.ID, Text: allowed.Name})
				break
			}
		}
	}

	if len(items) == 0 {
		current := a.agent.Model()
		if i := slices.IndexFunc(code.AvailableModels, func(am code.Model) bool { return am.ID == current }); i >= 0 {
			items = append(items, PickerItem{
				ID:   current,
				Text: code.AvailableModels[i].Name,
			})
		}
	}

	if len(items) == 0 {
		return
	}

	a.showPicker("Select Model", items, a.agent.Model(), func(item PickerItem) {
		a.setModel(item.ID)
		a.updateStatusBar()
	})
}

func (a *App) cycleModel() {
	go func() {
		apiModels, err := a.agent.Models(a.ctx)
		if err != nil {
			return
		}

		var models []string

		for _, allowed := range code.AvailableModels {
			for _, m := range apiModels {
				if m.ID == allowed.ID {
					models = append(models, m.ID)
					break
				}
			}
		}

		if len(models) <= 1 {
			return
		}

		for i, m := range models {
			if m == a.agent.Model() {
				a.setModel(models[(i+1)%len(models)])
				break
			}
		}

		a.app.QueueUpdateDraw(func() {
			a.updateStatusBar()
		})
	}()
}

func (a *App) setModel(model string) {
	a.agent.Config.Model = func() string { return model }
}
