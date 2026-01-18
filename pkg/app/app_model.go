package app

import (
	"slices"

	"github.com/adrianliechti/wingman-cli/pkg/config"
)

func (a *App) autoSelectModel() {
	if a.config.Model != "" {
		return
	}

	// Fetch models from API
	apiModels, err := a.config.Client.Models.List(a.ctx)

	if err != nil {
		return
	}

	// Find first available model matching our priority list
	for _, allowed := range config.AvailableModels {
		for _, model := range apiModels.Data {
			if model.ID == allowed {
				a.config.Model = model.ID
				return
			}
		}
	}

	// If no model matches priority list, use first available model
	if len(apiModels.Data) > 0 {
		a.config.Model = apiModels.Data[0].ID
	}
}

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
