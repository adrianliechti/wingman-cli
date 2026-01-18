package app

import (
	"slices"

	"github.com/adrianliechti/wingman-cli/pkg/config"
)

func (a *App) showModelPicker() {
	// Fetch models from API
	apiModels, err := a.config.Client.Models.List(a.ctx)
	if err != nil {
		return
	}

	// Filter to only available models
	var items []PickerItem
	for _, allowed := range config.AvailableModels {
		for _, model := range apiModels.Data {
			if model.ID == allowed {
				items = append(items, PickerItem{ID: model.ID, Text: model.ID})
				break
			}
		}
	}

	// If no models match, check if current model exists and add it
	if len(items) == 0 {
		if slices.Contains(config.AvailableModels, a.config.Model) {
			items = append(items, PickerItem{ID: a.config.Model, Text: a.config.Model})
		}
	}

	if len(items) == 0 {
		return
	}

	a.showPicker("Select Model", items, a.config.Model, func(item PickerItem) {
		a.config.Model = item.ID
		a.updateStatusBar()
	})
}
