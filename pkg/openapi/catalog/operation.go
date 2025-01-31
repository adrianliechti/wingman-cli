package catalog

import (
	"context"
)

type Operation struct {
	Name        string
	Description string

	Method string
	Path   string

	Queries []string

	Type   string
	Schema map[string]any
}

func (o *Operation) Execute(ctx context.Context, args any) (string, error) {
	return "", nil
}
