package agent

import "context"

type inputIDKey struct{}

// WithInputID correlates accepted user input with its retained conversation
// message. This is local metadata, never part of a model provider's payload.
func WithInputID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, inputIDKey{}, id)
}
func InputIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(inputIDKey{}).(string)
	return id
}
