package app

import (
	"slices"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

func (a *App) autoSelectModel() {
	if a.agent.Model != "" {
		return
	}

	// Fetch models from API
	apiModels, err := a.agent.Client.Models.List(a.ctx)

	if err != nil {
		return
	}

	// Find first available model matching our priority list
	for _, allowed := range agent.AvailableModels {
		for _, model := range apiModels.Data {
			if model.ID == allowed {
				a.agent.Model = model.ID

				return
			}
		}
	}

	// If no model matches priority list, use first available model
	if len(apiModels.Data) > 0 {
		a.agent.Model = apiModels.Data[0].ID
	}
}

func (a *App) showModelPicker() {
	// Fetch models from API
	apiModels, err := a.agent.Client.Models.List(a.ctx)

	if err != nil {
		return
	}

	// Filter to only available models
	var items []PickerItem

	for _, allowed := range agent.AvailableModels {
		for _, model := range apiModels.Data {
			if model.ID == allowed {
				items = append(items, PickerItem{ID: model.ID, Text: model.ID})
				break
			}
		}
	}

	// If no models match, check if current model exists and add it
	if len(items) == 0 {
		if slices.Contains(agent.AvailableModels, a.agent.Model) {
			items = append(items, PickerItem{ID: a.agent.Model, Text: a.agent.Model})
		}
	}

	if len(items) == 0 {
		return
	}

	a.showPicker("Select Model", items, a.agent.Model, func(item PickerItem) {
		a.agent.Model = item.ID
		a.updateStatusBar()
	})
}

func (a *App) cycleModel() {
	go func() {
		apiModels, err := a.agent.Client.Models.List(a.ctx)
		if err != nil {
			return
		}

		var models []string

		for _, allowed := range agent.AvailableModels {
			for _, model := range apiModels.Data {
				if model.ID == allowed {
					models = append(models, model.ID)
					break
				}
			}
		}

		if len(models) <= 1 {
			return
		}

		for i, m := range models {
			if m == a.agent.Model {
				a.agent.Model = models[(i+1)%len(models)]
				break
			}
		}

		a.app.QueueUpdateDraw(func() {
			a.updateStatusBar()
		})
	}()
}
