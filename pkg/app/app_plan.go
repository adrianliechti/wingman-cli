package app

import (
	"fmt"

	"github.com/adrianliechti/wingman-agent/pkg/agent/plan"
	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

func (a *App) enterPlanMode(announce bool) {
	if a.currentMode == ModePlan {
		return
	}

	planFile, err := a.ensurePlanFile()
	if err != nil {
		fmt.Fprint(a.chatView, a.formatNotice(fmt.Sprintf("Failed to enter plan mode: %v", err), theme.Default.Red))
		return
	}

	a.currentMode = ModePlan
	if a.agent != nil && a.agent.Environment != nil {
		a.agent.Environment.SetPlanMode(planFile)
	}
	a.updateStatusBar()

	if announce {
		fmt.Fprint(a.chatView, a.formatNotice(fmt.Sprintf("Entered plan mode. Plan: %s", planFile), theme.Default.Cyan))
	}
}

func (a *App) exitPlanMode(announce bool) {
	if a.currentMode == ModeAgent {
		return
	}

	a.currentMode = ModeAgent
	if a.agent != nil && a.agent.Environment != nil {
		a.agent.Environment.SetAgentMode()
	}
	a.updateStatusBar()

	if announce {
		message := "Returned to agent mode."
		if a.planFile != "" {
			message = fmt.Sprintf("Returned to agent mode. Active plan: %s", a.planFile)
		}
		fmt.Fprint(a.chatView, a.formatNotice(message, theme.Default.Cyan))
	}
}

func (a *App) ensurePlanFile() (string, error) {
	if a.planFile != "" {
		return a.planFile, nil
	}

	path, err := plan.Ensure(a.agent.Environment.MemoryDir())
	if err != nil {
		return "", err
	}

	a.planFile = path
	return path, nil
}

func (a *App) currentInstructions() string {
	return a.agent.BuildInstructions(a.currentMode == ModePlan, a.bridgeInstructions())
}

func (a *App) bridgeInstructions() string {
	if a.bridge == nil {
		return ""
	}

	return a.bridge.GetInstructions()
}

func (a *App) bridgeContext() string {
	if a.bridge == nil {
		return ""
	}

	return a.bridge.GetContext()
}
