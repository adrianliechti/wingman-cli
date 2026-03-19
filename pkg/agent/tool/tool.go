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
}

type Environment struct {
	Date string

	OS   string
	Arch string

	Root    *os.Root
	Scratch *os.Root

	PromptUser   func(prompt string) (bool, error)
	DiagnoseFile func(ctx context.Context, path string) string
}

func (e *Environment) WorkingDir() string {
	return e.Root.Name()
}

func (e *Environment) ScratchDir() string {
	return e.Scratch.Name()
}