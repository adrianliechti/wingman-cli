package code

import (
	"context"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	core "github.com/adrianliechti/wingman-agent/pkg/code"
)

type modelFetcher interface {
	FetchModels(context.Context)
}

type sessionSaver interface {
	Save(string) error
}

type recapProvider interface {
	Recap(context.Context, string) (string, error)
}

type contextStatsProvider interface {
	ContextStats(string) (agent.ContextStats, bool)
}

type taskProvider interface {
	Tasks(string) *task.Registry
	RunningTaskCount() int
}

type toolProvider interface {
	Tools(string) []tool.Tool
}

type uiAwareAgent interface {
	SetUI(core.UI)
}

func setAgentUI(a core.Agent, ui core.UI) {
	if aware, ok := a.(uiAwareAgent); ok {
		aware.SetUI(ui)
	}
}
