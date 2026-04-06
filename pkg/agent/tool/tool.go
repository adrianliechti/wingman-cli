package tool

import (
	"context"
	"os"
	"sync"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tracker"
)

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, env *Environment, args map[string]any) (string, error)
	Hidden      bool

	ConcurrencySafe     bool
	ConcurrencySafeFunc func(args map[string]any) bool
}

func (t Tool) AllowsConcurrentExecution(args map[string]any) bool {
	if t.ConcurrencySafeFunc != nil {
		return t.ConcurrencySafeFunc(args)
	}

	return t.ConcurrencySafe
}

type Environment struct {
	OS   string
	Arch string

	Root    *os.Root
	Memory  *os.Root
	Scratch *os.Root

	Tracker *tracker.Tracker

	planMu   sync.RWMutex
	planning bool
	planFile string

	AskUser      func(question string) (string, error)
	PromptUser   func(prompt string) (bool, error)
	DiagnoseFile func(ctx context.Context, path string) string
	StatusUpdate func(status string)
}

func (e *Environment) WorkingDir() string {
	return e.Root.Name()
}

func (e *Environment) ScratchDir() string {
	return e.Scratch.Name()
}

func (e *Environment) MemoryDir() string {
	return e.Memory.Name()
}

func (e *Environment) SetPlanMode(planFile string) {
	if e == nil {
		return
	}

	e.planMu.Lock()
	e.planning = true
	e.planFile = planFile
	e.planMu.Unlock()
}

func (e *Environment) SetAgentMode() {
	if e == nil {
		return
	}

	e.planMu.Lock()
	e.planning = false
	e.planMu.Unlock()
}

func (e *Environment) IsPlanning() bool {
	if e == nil {
		return false
	}

	e.planMu.RLock()
	planning := e.planning
	e.planMu.RUnlock()

	return planning
}

func (e *Environment) PlanFile() string {
	if e == nil {
		return ""
	}

	e.planMu.RLock()
	planFile := e.planFile
	e.planMu.RUnlock()

	return planFile
}
