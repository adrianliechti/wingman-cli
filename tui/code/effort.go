package code

func (a *App) showEffortPicker() {
	items := []PickerItem{
		{ID: "auto", Text: "Auto"},
		{ID: "low", Text: "Low"},
		{ID: "medium", Text: "Medium"},
		{ID: "high", Text: "High"},
	}

	current := "auto"
	if a.agent.Effort != nil {
		if v := a.agent.Effort(); v != "" {
			current = v
		}
	}

	a.showPicker("Select Effort", items, current, func(item PickerItem) {
		a.setEffort(item.ID)
		a.updateStatusBar()
	})
}

func (a *App) setEffort(effort string) {
	if effort == "" || effort == "auto" {
		a.agent.Config.Effort = nil
		return
	}

	a.agent.Config.Effort = func() string { return effort }
}
