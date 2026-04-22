package channel

import (
	"context"
	"io"
	"time"
)

// Message represents an inbound or outbound message in a chat.
type Message struct {
	ChatID    string
	Sender    string
	Content   string
	Timestamp time.Time
	IsBot     bool
}

// MessageHandler is called by a channel when a new message arrives.
type MessageHandler func(ctx context.Context, msg Message)

// Channel is the interface for message sources and sinks.
// Inspired by nanoclaw's channel abstraction — each channel (CLI, WhatsApp,
// Telegram, ...) implements this to deliver and receive messages.
type Channel interface {
	// Name returns the channel identifier (e.g. "cli", "whatsapp").
	Name() string

	// Start begins listening for messages and calls handler for each one.
	// It blocks until the context is cancelled or the channel is exhausted.
	Start(ctx context.Context, handler MessageHandler) error

	// Send delivers a message to the given chat.
	Send(ctx context.Context, chatID string, text string) error

	// SendStream returns a writer that streams partial output to the chat.
	// Callers must close the writer when done.
	SendStream(ctx context.Context, chatID string) (io.WriteCloser, error)
}
