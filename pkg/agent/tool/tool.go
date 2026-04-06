package tool

import (
	"context"
	"os"
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
	Scratch *os.Root
	Memory  *os.Root

	ReadTracker *ReadTracker
	Session     *SessionState

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
	if e.Memory == nil {
		return ""
	}
	return e.Memory.Name()
}

func (e *Environment) IsPlanning() bool {
	if e == nil || e.Session == nil {
		return false
	}

	return e.Session.IsPlanning()
}

func (e *Environment) PlanFile() string {
	if e == nil || e.Session == nil {
		return ""
	}

	return e.Session.PlanFile()
}
