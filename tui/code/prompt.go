package code

import (
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func (a *App) currentInstructions() string {
	data := a.agent.InstructionsData()
	data.PlanMode = a.currentMode == ModePlan

	return code.BuildInstructions(data)
}

func (a *App) bridgeInstructions() string {
	if a.agent.Bridge == nil {
		return ""
	}

	return a.agent.Bridge.GetInstructions()
}

func (a *App) bridgeContext() string {
	if a.agent.Bridge == nil {
		return ""
	}

	return a.agent.Bridge.GetContext()
}
