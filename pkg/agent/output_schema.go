package agent

import "context"

type outputSchemaKey struct{}

// WithOutputSchema requests structured output for a turn. An empty schema uses
// the provider's native JSON-object mode; a non-empty schema uses strict JSON
// Schema output. A nil schema is treated as no structured-output request.
func WithOutputSchema(ctx context.Context, schema map[string]any) context.Context {
	return context.WithValue(ctx, outputSchemaKey{}, schema)
}

// OutputSchemaFromContext returns the turn's requested structured output.
func OutputSchemaFromContext(ctx context.Context) (map[string]any, bool) {
	schema, ok := ctx.Value(outputSchemaKey{}).(map[string]any)
	return schema, ok && schema != nil
}
