package app

import (
	"fmt"

	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

func (a *App) enterPlanMode(announce bool) {
	if a.currentMode == ModePlan {
		return
	}

	a.agent.PlanMode = true
	a.currentMode = ModePlan
	a.updateStatusBar()

	if announce {
		fmt.Fprint(a.chatView, a.formatNotice("Entered plan mode. Read-only tools only.", theme.Default.Cyan))
	}
}

func (a *App) exitPlanMode(announce bool) {
	if a.currentMode == ModeAgent {
		return
	}

	a.agent.PlanMode = false
	a.currentMode = ModeAgent
	a.updateStatusBar()

	if announce {
		fmt.Fprint(a.chatView, a.formatNotice("Returned to agent mode.", theme.Default.Cyan))
	}
}
