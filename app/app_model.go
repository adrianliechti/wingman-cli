package app

import (
	"slices"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

func (a *App) autoSelectModel() {
	if a.agent.Model != nil && a.agent.Model() != "" {
		return
	}

	models, err := a.agent.Models(a.ctx)

	if err != nil {
		return
	}

	for _, allowed := range agent.AvailableModels {
		for _, m := range models {
			if m.ID == allowed {
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

	for _, allowed := range agent.AvailableModels {
		for _, m := range models {
			if m.ID == allowed {
				items = append(items, PickerItem{ID: m.ID, Text: m.ID})
				break
			}
		}
	}

	if len(items) == 0 {
		if slices.Contains(agent.AvailableModels, a.agent.Model()) {
			items = append(items, PickerItem{ID: a.agent.Model(), Text: a.agent.Model()})
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

		for _, allowed := range agent.AvailableModels {
			for _, m := range apiModels {
				if m.ID == allowed {
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
