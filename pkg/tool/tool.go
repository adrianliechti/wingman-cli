package tool

import (
	"context"
	"os"

	"github.com/adrianliechti/wingman-cli/pkg/plan"
)

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, env *Environment, args map[string]any) (string, error)
	Hidden      bool
}

type Environment struct {
	OS   string
	Arch string

	Root    *os.Root
	Scratch *os.Root

	Plan       *plan.Plan
	PromptUser func(prompt string) (bool, error)
}

func (e *Environment) WorkingDir() string {
	return e.Root.Name()
}

func (e *Environment) ScratchDir() string {
	return e.Scratch.Name()
}
