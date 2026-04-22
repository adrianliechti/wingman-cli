package app

func (a *App) enterPlanMode() {
	if a.currentMode == ModePlan {
		return
	}

	a.agent.PlanMode = true
	a.currentMode = ModePlan
	a.updateStatusBar()
}

func (a *App) exitPlanMode() {
	if a.currentMode == ModeAgent {
		return
	}

	a.agent.PlanMode = false
	a.currentMode = ModeAgent
	a.updateStatusBar()
}
