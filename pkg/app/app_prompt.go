package app

import (
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/agent/skill"
)

func (a *App) currentInstructions() string {
	env := a.agent.Environment

	base := prompt.Instructions

	planMode := a.currentMode == ModePlan

	if planMode {
		base = prompt.Planning
	}

	return prompt.BuildInstructions(base, prompt.SectionData{
		PlanMode:            planMode,
		Date:                time.Now().Format("January 2, 2006"),
		OS:                  env.OS,
		Arch:                env.Arch,
		WorkingDir:          env.RootDir(),
		MemoryDir:           env.MemoryDir(),
		MemoryContent:       env.MemoryContent(),
		PlanFile:            env.PlanFile(),
		PlanContent:         env.PlanContent(),
		Skills:              skill.FormatForPrompt(a.agent.Skills),
		ProjectInstructions: agent.ReadAgentsFile(env.RootDir()),
		BridgeInstructions:  a.bridgeInstructions(),
	})
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
