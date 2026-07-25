package code

import (
	"context"
	"iter"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

type SessionInfo struct {
	ID        string
	Title     string
	UpdatedAt time.Time
}

type Mode struct {
	ID          string
	Name        string
	Description string
}

type Agent interface {
	Name() string

	Workspace() *Workspace

	Models(sessionID string) (available []model.Model, current string)

	SetModel(ctx context.Context, sessionID, id string) error

	Effort(sessionID string) (current string, options []string)

	SetEffort(ctx context.Context, sessionID, value string) error

	Modes(sessionID string) (available []Mode, current string)

	SetMode(ctx context.Context, sessionID, modeID string) error

	ListSessions(ctx context.Context) ([]SessionInfo, error)

	NewSession(ctx context.Context) (string, error)

	LoadSession(ctx context.Context, id string) error

	DeleteSession(ctx context.Context, id string) error

	Messages(sessionID string) []agent.Message

	Usage(sessionID string) agent.Usage

	// Send starts one turn and returns its event stream. Immediate validation
	// and busy-session failures are returned directly; errors that happen after
	// the turn starts are yielded by the stream. Send never queues implicitly.
	// Once Send returns successfully, the agent owns input and callers may reuse
	// or mutate their slice.
	Send(ctx context.Context, sessionID string, input []agent.Content) (iter.Seq2[agent.Message, error], error)

	Cancel(sessionID string)

	Close() error
}

var (
	ErrTurnInProgress = agent.ErrTurnInProgress
	ErrEmptyInput     = agent.ErrEmptyInput
)

type SessionLoadStreamer interface {
	LoadSessionStream(ctx context.Context, id string) iter.Seq2[[]agent.Message, error]
}
