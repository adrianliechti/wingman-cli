package code

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	wingmancode "github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

type uiTestAgent struct {
	workspace *wingmancode.Workspace
	messages  []agent.Message
	revision  uint64
	snapshots int
	model     string
	effort    string
	efforts   []string
	mode      string
	sessions  []wingmancode.SessionInfo
}

func newUITestAgent(messages []agent.Message) *uiTestAgent {
	return &uiTestAgent{
		workspace: &wingmancode.Workspace{RootPath: "/workspace"},
		messages:  messages,
		model:     "gpt-5.6-sol",
		effort:    "medium",
		efforts:   []string{"low", "medium", "high"},
		mode:      "agent",
	}
}

func (a *uiTestAgent) Name() string                      { return wingmancode.BuiltinAgentName }
func (a *uiTestAgent) Workspace() *wingmancode.Workspace { return a.workspace }
func (a *uiTestAgent) Models(string) ([]model.Model, string) {
	return []model.Model{{ID: a.model, Name: "GPT 5.6 Sol"}}, a.model
}
func (a *uiTestAgent) SetModel(_ context.Context, _ string, value string) error {
	a.model = value
	return nil
}
func (a *uiTestAgent) Effort(string) (string, []string) {
	return a.effort, append([]string(nil), a.efforts...)
}
func (a *uiTestAgent) SetEffort(_ context.Context, _ string, value string) error {
	a.effort = value
	return nil
}
func (a *uiTestAgent) Modes(string) ([]wingmancode.Mode, string) {
	return []wingmancode.Mode{
		{ID: "agent", Name: "Agent"},
		{ID: "plan", Name: "Plan"},
		wingmancode.UnattendedMode(),
	}, a.mode
}
func (a *uiTestAgent) SetMode(_ context.Context, _, mode string) error {
	a.mode = mode
	return nil
}
func (a *uiTestAgent) ListSessions(context.Context) ([]wingmancode.SessionInfo, error) {
	return a.sessions, nil
}
func (a *uiTestAgent) NewSession(context.Context) (string, error)  { return "new", nil }
func (a *uiTestAgent) LoadSession(context.Context, string) error   { return nil }
func (a *uiTestAgent) DeleteSession(context.Context, string) error { return nil }
func (a *uiTestAgent) Messages(string) []agent.Message             { return a.messages }
func (a *uiTestAgent) HistorySnapshot(string) wingmancode.HistorySnapshot {
	a.snapshots++
	return wingmancode.HistorySnapshot{Messages: agent.CloneMessages(a.messages), Revision: a.revision}
}
func (a *uiTestAgent) HistoryVersion(string) wingmancode.HistoryVersion {
	return wingmancode.HistoryVersion{Revision: a.revision, MessageCount: len(a.messages)}
}
func (a *uiTestAgent) Usage(string) agent.Usage { return agent.Usage{} }
func (a *uiTestAgent) Send(context.Context, string, []agent.Content) (iter.Seq2[agent.Message, error], error) {
	return func(func(agent.Message, error) bool) {}, nil
}
func (a *uiTestAgent) Cancel(string) {}
func (a *uiTestAgent) Close() error  { return nil }
