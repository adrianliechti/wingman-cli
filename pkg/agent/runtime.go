package agent

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
)

// RuntimeEventType identifies an immutable fact in an agent's runtime ledger.
// Messages form canonical conversation history; context checkpoints only
// replace the provider-facing projection and never remove canonical events.
type RuntimeEventType string

const (
	EventMessage           RuntimeEventType = "message"
	EventContextCheckpoint RuntimeEventType = "context_checkpoint"
	EventUsage             RuntimeEventType = "usage"
	EventTurnStarted       RuntimeEventType = "turn_started"
	EventTurnTerminal      RuntimeEventType = "turn_terminal"
	EventRunStarted        RuntimeEventType = "run_started"
	EventRunTerminal       RuntimeEventType = "run_terminal"
	EventToolStarted       RuntimeEventType = "tool_started"
	EventToolTerminal      RuntimeEventType = "tool_terminal"
)

type RuntimeStatus string

const (
	RuntimeCompleted   RuntimeStatus = "completed"
	RuntimeFailed      RuntimeStatus = "failed"
	RuntimeInterrupted RuntimeStatus = "interrupted"
)

// RuntimeTerminal is the durable outcome of a turn, provider run, or tool
// operation. OutcomeUncertain is set when a process stopped after a possibly
// mutating tool began but before its terminal fact reached the ledger.
type RuntimeTerminal struct {
	Status           RuntimeStatus `json:"status"`
	Error            string        `json:"error,omitempty"`
	OutcomeUncertain bool          `json:"outcome_uncertain,omitempty"`
}

type RuntimeTool struct {
	CallID  string `json:"call_id,omitempty"`
	Name    string `json:"name"`
	Args    string `json:"args,omitempty"`
	Effect  string `json:"effect,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

// RuntimeEvent is deliberately a small tagged union. Sequence is assigned by
// Agent immediately before durable append; ID identifies this individual
// fact, while TurnID/RunID/OperationID identify lifecycle entities.
type RuntimeEvent struct {
	Sequence uint64           `json:"sequence"`
	ID       string           `json:"id"`
	Type     RuntimeEventType `json:"type"`
	At       time.Time        `json:"at"`

	TurnID      string `json:"turn_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	Model       string `json:"model,omitempty"`

	Message       *Message         `json:"message,omitempty"`
	Context       []Message        `json:"context,omitempty"`
	ContextReason string           `json:"context_reason,omitempty"`
	Usage         *Usage           `json:"usage,omitempty"`
	Terminal      *RuntimeTerminal `json:"terminal,omitempty"`
	Tool          *RuntimeTool     `json:"tool,omitempty"`
}

// EventRecorder is the durability boundary. Implementations must append the
// whole batch in order or return an error without reporting success. Agent
// applies events to its in-memory projections only after this call succeeds.
type EventRecorder interface {
	AppendEvents(events []RuntimeEvent) error
}

type EventRecorderFunc func(events []RuntimeEvent) error

func (f EventRecorderFunc) AppendEvents(events []RuntimeEvent) error { return f(events) }

type runtimeEventIndex struct {
	starts       map[string]struct{}
	terminals    map[string]struct{}
	eventIDs     map[string]struct{}
	eventCount   int
	lastSequence uint64
}

func newRuntimeEventIndex() runtimeEventIndex {
	return runtimeEventIndex{
		starts:    make(map[string]struct{}),
		terminals: make(map[string]struct{}),
		eventIDs:  make(map[string]struct{}),
	}
}

func cloneRuntimeEvents(events []RuntimeEvent) []RuntimeEvent {
	out := make([]RuntimeEvent, len(events))
	for i := range events {
		out[i] = events[i]
		if events[i].Message != nil {
			message := CloneMessages([]Message{*events[i].Message})[0]
			out[i].Message = &message
		}
		out[i].Context = CloneMessages(events[i].Context)
		if events[i].Usage != nil {
			usage := *events[i].Usage
			out[i].Usage = &usage
		}
		if events[i].Terminal != nil {
			terminal := *events[i].Terminal
			out[i].Terminal = &terminal
		}
		if events[i].Tool != nil {
			tool := *events[i].Tool
			out[i].Tool = &tool
		}
	}
	return out
}

func (a *Agent) recordEvents(events ...RuntimeEvent) error {
	if len(events) == 0 {
		return nil
	}

	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.ensureStateLocked()
	if err := a.ensureRuntimeIndexLocked(); err != nil {
		return err
	}

	next := a.runtimeIndex.lastSequence
	prepared := cloneRuntimeEvents(events)
	for i := range prepared {
		next++
		prepared[i].Sequence = next
		if prepared[i].ID == "" {
			prepared[i].ID = uuid.NewString()
		}
		if prepared[i].At.IsZero() {
			prepared[i].At = time.Now().UTC()
		}
	}
	if err := validateRuntimeBatch(prepared, a.runtimeIndex); err != nil {
		return err
	}

	if a.Recorder != nil {
		if err := a.Recorder.AppendEvents(cloneRuntimeEvents(prepared)); err != nil {
			return fmt.Errorf("append runtime ledger: %w", err)
		}
	}

	for _, event := range prepared {
		a.applyEventLocked(event)
		a.runtimeIndex.observe(event)
	}
	return nil
}

func validateRuntimeEvents(events []RuntimeEvent) error {
	index := newRuntimeEventIndex()
	if err := validateRuntimeBatch(events, index); err != nil {
		return err
	}
	for _, event := range events {
		index.observe(event)
	}
	return nil
}

func containsKey(persisted, pending map[string]struct{}, key string) bool {
	if _, ok := persisted[key]; ok {
		return true
	}
	_, ok := pending[key]
	return ok
}

func validateRuntimeBatch(events []RuntimeEvent, index runtimeEventIndex) error {
	starts := make(map[string]struct{})
	terminals := make(map[string]struct{})
	eventIDs := make(map[string]struct{})
	previous := index.lastSequence
	for _, event := range events {
		if event.Sequence == 0 || event.Sequence <= previous {
			return fmt.Errorf("runtime events are not strictly ordered at sequence %d", event.Sequence)
		}
		previous = event.Sequence
		if event.ID == "" {
			return fmt.Errorf("runtime event %d has no id", event.Sequence)
		}
		if containsKey(index.eventIDs, eventIDs, event.ID) {
			return fmt.Errorf("runtime event id %q is duplicated", event.ID)
		}
		eventIDs[event.ID] = struct{}{}
		switch event.Type {
		case EventMessage:
			if event.Message == nil {
				return fmt.Errorf("message event %d has no message", event.Sequence)
			}
		case EventContextCheckpoint:
		case EventUsage:
			if event.Usage == nil {
				return fmt.Errorf("usage event %d has no usage", event.Sequence)
			}
		case EventTurnStarted, EventRunStarted, EventToolStarted:
			entity := runtimeEntityID(event)
			key := string(event.Type) + "\x00" + entity
			if entity == "" {
				return fmt.Errorf("%s requires an entity id", event.Type)
			}
			if containsKey(index.starts, starts, key) {
				return fmt.Errorf("%s %s was started more than once", event.Type, entity)
			}
			starts[key] = struct{}{}
		case EventTurnTerminal, EventRunTerminal, EventToolTerminal:
			entity := runtimeEntityID(event)
			if entity == "" || event.Terminal == nil || !validRuntimeStatus(event.Terminal.Status) {
				return fmt.Errorf("%s requires an entity id and valid terminal status", event.Type)
			}
			terminalKey := string(event.Type) + "\x00" + entity
			if containsKey(index.terminals, terminals, terminalKey) {
				return fmt.Errorf("%s %s already has a terminal fact", event.Type, entity)
			}
			startKey := string(startTypeForTerminal(event.Type)) + "\x00" + entity
			if !containsKey(index.starts, starts, startKey) {
				return fmt.Errorf("%s %s has no matching start fact", event.Type, entity)
			}
			terminals[terminalKey] = struct{}{}
		default:
			return fmt.Errorf("unknown runtime event type %q", event.Type)
		}
	}
	return nil
}

func (index *runtimeEventIndex) observe(event RuntimeEvent) {
	index.eventIDs[event.ID] = struct{}{}
	index.eventCount++
	index.lastSequence = event.Sequence
	switch event.Type {
	case EventTurnStarted, EventRunStarted, EventToolStarted:
		index.starts[string(event.Type)+"\x00"+runtimeEntityID(event)] = struct{}{}
	case EventTurnTerminal, EventRunTerminal, EventToolTerminal:
		index.terminals[string(event.Type)+"\x00"+runtimeEntityID(event)] = struct{}{}
	}
}

func (a *Agent) ensureRuntimeIndexLocked() error {
	if a.runtimeIndexSet && a.runtimeIndex.eventCount == len(a.Events) && a.runtimeIndex.lastSequence == a.lastSequenceLocked() {
		return nil
	}
	index := newRuntimeEventIndex()
	if err := validateRuntimeBatch(a.Events, index); err != nil {
		return err
	}
	for _, event := range a.Events {
		index.observe(event)
	}
	a.runtimeIndex = index
	a.runtimeIndexSet = true
	return nil
}

func startTypeForTerminal(t RuntimeEventType) RuntimeEventType {
	switch t {
	case EventTurnTerminal:
		return EventTurnStarted
	case EventRunTerminal:
		return EventRunStarted
	case EventToolTerminal:
		return EventToolStarted
	default:
		return ""
	}
}

func validRuntimeStatus(status RuntimeStatus) bool {
	switch status {
	case RuntimeCompleted, RuntimeFailed, RuntimeInterrupted:
		return true
	default:
		return false
	}
}

func isTerminalEvent(t RuntimeEventType) bool {
	switch t {
	case EventTurnTerminal, EventRunTerminal, EventToolTerminal:
		return true
	default:
		return false
	}
}

func runtimeEntityID(event RuntimeEvent) string {
	switch event.Type {
	case EventTurnStarted, EventTurnTerminal:
		return event.TurnID
	case EventRunStarted, EventRunTerminal:
		return event.RunID
	case EventToolStarted, EventToolTerminal:
		return event.OperationID
	default:
		return ""
	}
}

func (a *Agent) lastSequenceLocked() uint64 {
	if len(a.Events) == 0 {
		return 0
	}
	return a.Events[len(a.Events)-1].Sequence
}

func (a *Agent) applyEventLocked(event RuntimeEvent) {
	a.Events = append(a.Events, event)
	switch event.Type {
	case EventMessage:
		if event.Message != nil {
			message := CloneMessages([]Message{*event.Message})[0]
			a.Messages = append(a.Messages, message)
			a.contextMessages = append(a.contextMessages, CloneMessages([]Message{message})[0])
			a.contextSet = true
		}
	case EventContextCheckpoint:
		a.contextMessages = CloneMessages(event.Context)
		a.contextSet = true
		a.ContextRevision++
	case EventUsage, EventRunTerminal:
		if event.Usage != nil {
			a.addUsageLocked(*event.Usage)
		}
	}
}

func (a *Agent) addUsageLocked(delta Usage) {
	a.Usage.InputTokens += delta.InputTokens
	a.Usage.OutputTokens += delta.OutputTokens
	a.Usage.ReasoningTokens += delta.ReasoningTokens
	a.Usage.CacheReadInputTokens += delta.CacheReadInputTokens
	a.Usage.CacheCreationInputTokens += delta.CacheCreationInputTokens
	if delta.LastInputTokens > 0 {
		a.Usage.LastInputTokens = delta.LastInputTokens
	}
	if delta.ContextWindow > 0 {
		a.Usage.ContextWindow = delta.ContextWindow
	}
}

func (a *Agent) replaceContext(reason string, messages []Message) error {
	// A context checkpoint means some part of the provider-visible history was
	// rewritten. Opaque reasoning is bound to the exact model and prefix that
	// produced it, so replaying any retained payload after a rewrite is unsafe:
	// providers may reject it, and removing one foreign block also invalidates
	// later same-model blocks whose prefix included it. Keep summaries in the
	// recovery projection for continuity, but leave the canonical event history
	// untouched for display and audit.
	messages = withoutReplayableReasoning(messages)
	return a.recordEvents(RuntimeEvent{
		Type:          EventContextCheckpoint,
		Context:       CloneMessages(messages),
		ContextReason: reason,
	})
}

func withoutReplayableReasoning(messages []Message) []Message {
	cleaned := CloneMessages(messages)
	for i := range cleaned {
		for j := range cleaned[i].Content {
			reasoning := cleaned[i].Content[j].Reasoning
			if reasoning == nil {
				continue
			}
			reasoning.Content = ""
			reasoning.Model = ""
		}
	}
	return cleaned
}

// ReconcileInterrupted closes lifecycle entities that were open when the
// previous process stopped. It never retries work. An open non-read-only tool
// is explicitly recorded as uncertain because its side effect may have
// happened before the crash.
func (a *Agent) ReconcileInterrupted(reason string) error {
	if reason == "" {
		reason = "process stopped before a terminal fact was recorded"
	}

	a.stateMu.Lock()
	a.ensureStateLocked()
	events := cloneRuntimeEvents(a.Events)
	a.stateMu.Unlock()

	openTurns := map[string]RuntimeEvent{}
	openRuns := map[string]RuntimeEvent{}
	openTools := map[string]RuntimeEvent{}
	for _, event := range events {
		switch event.Type {
		case EventTurnStarted:
			openTurns[event.TurnID] = event
		case EventTurnTerminal:
			delete(openTurns, event.TurnID)
		case EventRunStarted:
			openRuns[event.RunID] = event
		case EventRunTerminal:
			delete(openRuns, event.RunID)
		case EventToolStarted:
			openTools[event.OperationID] = event
		case EventToolTerminal:
			delete(openTools, event.OperationID)
		}
	}

	var reconciled []RuntimeEvent
	for _, started := range slices.SortedFunc(maps.Values(openTools), func(a, b RuntimeEvent) int {
		return cmp.Compare(a.Sequence, b.Sequence)
	}) {
		uncertain := started.Tool == nil || started.Tool.Effect != "read_only"
		reconciled = append(reconciled, RuntimeEvent{
			Type: EventToolTerminal, TurnID: started.TurnID, OperationID: started.OperationID,
			Tool:     started.Tool,
			Terminal: &RuntimeTerminal{Status: RuntimeInterrupted, Error: reason, OutcomeUncertain: uncertain},
		})
	}
	for _, started := range slices.SortedFunc(maps.Values(openRuns), func(a, b RuntimeEvent) int {
		return cmp.Compare(a.Sequence, b.Sequence)
	}) {
		reconciled = append(reconciled, RuntimeEvent{
			Type: EventRunTerminal, TurnID: started.TurnID, RunID: started.RunID, Model: started.Model,
			Terminal: &RuntimeTerminal{Status: RuntimeInterrupted, Error: reason},
		})
	}
	for _, started := range slices.SortedFunc(maps.Values(openTurns), func(a, b RuntimeEvent) int {
		return cmp.Compare(a.Sequence, b.Sequence)
	}) {
		reconciled = append(reconciled, RuntimeEvent{
			Type: EventTurnTerminal, TurnID: started.TurnID,
			Terminal: &RuntimeTerminal{Status: RuntimeInterrupted, Error: reason},
		})
	}
	return a.recordEvents(reconciled...)
}
