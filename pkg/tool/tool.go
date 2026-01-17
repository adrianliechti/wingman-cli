package tool

import "os"

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(env *Environment, args map[string]any) (string, error)
}

type Environment struct {
	OS   string
	Arch string

	Root    *os.Root
	Scratch *os.Root

	PromptUser func(prompt string) (bool, error)
}

func (e *Environment) WorkingDir() string {
	return e.Root.Name()
}

func (e *Environment) ScratchDir() string {
	return e.Scratch.Name()
}
