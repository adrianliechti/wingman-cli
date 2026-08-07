package code

import (
	"context"
	"iter"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
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

const (
	AgentModeID      = "agent"
	PlanModeID       = "plan"
	UnattendedModeID = "unattended"
)

func UnattendedMode() Mode {
	return Mode{
		ID:          UnattendedModeID,
		Name:        "Unattended",
		Description: "Auto-approves actions and makes reasonable assumptions instead of asking.",
	}
}

// UnattendedElicitation resolves questions without UI. Defaults win, followed
// by the first (recommended) option; required free-text questions are declined
// rather than answered with fabricated input.
func UnattendedElicitation(req tool.ElicitRequest) tool.ElicitResult {
	content := map[string]any{}
	for _, field := range req.Fields {
		if field.CustomAnswerFor != "" {
			continue
		}
		switch {
		case field.Default != nil:
			content[field.Name] = field.Default
		case len(field.Enum) > 0 && field.Multiple:
			content[field.Name] = []string{field.Enum[0]}
		case len(field.Enum) > 0:
			content[field.Name] = field.Enum[0]
		case field.Type == "boolean":
			content[field.Name] = true
		case field.Required:
			return tool.ElicitResult{Action: tool.ElicitDecline}
		}
	}
	return tool.ElicitResult{Action: tool.ElicitAccept, Content: content}
}

type Command struct {
	Name        string
	Description string
	InputHint   string
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

// CommandProvider exposes agent-defined slash commands for clients that can
// present command discovery alongside their own local commands.
type CommandProvider interface {
	Commands(sessionID string) []Command
}
