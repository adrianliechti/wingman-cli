package tool

import (
	"context"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
)

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, env *env.Environment, args map[string]any) (string, error)
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
