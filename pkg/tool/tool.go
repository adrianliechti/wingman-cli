package tool

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
)

type Provider interface {
	Tools(ctx context.Context) ([]Tool, error)
}

type Tool struct {
	Name        string
	Description string

	Schema *Schema

	ToolHandler
}

type Schema = jsonschema.Schema

type ToolHandler = func(ctx context.Context, args map[string]any) (any, error)
