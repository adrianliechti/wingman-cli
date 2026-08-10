package agent

import "context"

// StreamEvent describes a lifecycle boundary in an Agent.Send stream. These
// events are separate from Message because they are transport concerns, not
// conversational content that can be retained or sent back to a model.
type StreamEvent uint8

const (
	// StreamEventReset asks a consumer to discard visible deltas from the
	// current failed attempt before the agent retries the request.
	StreamEventReset StreamEvent = iota + 1
	// StreamEventCommit marks the current streamed attempt as accepted into
	// retained history. A later retry reset must not discard its output.
	StreamEventCommit
)

// StreamEventHandlers declares the lifecycle operations a stream consumer can
// actually perform. In particular, a non-nil Reset is a capability promise:
// the consumer can discard every delta from the failed attempt before Send
// retries it.
type StreamEventHandlers struct {
	Reset  func()
	Commit func()
}

type streamEventHandlersKey struct{}

// WithStreamEventHandlers installs synchronous lifecycle operations for an
// Agent.Send consumer. Handlers must return quickly.
func WithStreamEventHandlers(ctx context.Context, handlers StreamEventHandlers) context.Context {
	if handlers.Reset == nil && handlers.Commit == nil {
		return ctx
	}
	return context.WithValue(ctx, streamEventHandlersKey{}, handlers)
}

// EmitStreamEvent synchronously publishes a lifecycle event to the sink in
// ctx. It reports whether the consumer implements that exact event; agents
// must not retry after visible partial output unless Reset is implemented.
func EmitStreamEvent(ctx context.Context, event StreamEvent) bool {
	handlers, _ := ctx.Value(streamEventHandlersKey{}).(StreamEventHandlers)
	var handler func()
	switch event {
	case StreamEventReset:
		handler = handlers.Reset
	case StreamEventCommit:
		handler = handlers.Commit
	}
	if handler == nil {
		return false
	}
	handler()
	return true
}
