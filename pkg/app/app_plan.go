package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/memory"
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
	if a.agent != nil && a.agent.Environment != nil && a.agent.Environment.Session != nil {
		a.agent.Environment.Session.SetPlanMode(planFile)
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
	if a.agent != nil && a.agent.Environment != nil && a.agent.Environment.Session != nil {
		a.agent.Environment.Session.SetAgentMode()
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
	if a.planFile == "" {
		memoryDir := a.agent.Environment.MemoryDir()
		if memoryDir == "" {
			memoryDir = memory.Dir(a.agent.Environment.WorkingDir())
		}

		if memoryDir == "" {
			return "", fmt.Errorf("memory directory is not available")
		}

		if err := memory.EnsureDir(memoryDir); err != nil {
			return "", err
		}

		a.planFile = memory.PlanPath(memoryDir)
	}

	data, err := os.ReadFile(a.planFile)
	if err == nil && strings.TrimSpace(string(data)) != "" {
		return a.planFile, nil
	}

	if err := os.WriteFile(a.planFile, []byte(memory.NewPlanContent()), 0644); err != nil {
		return "", err
	}

	return a.planFile, nil
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
