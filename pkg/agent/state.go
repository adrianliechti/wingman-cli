package agent

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

type State struct {
	Events          []RuntimeEvent `json:"events,omitempty"`
	Usage           Usage          `json:"usage"`
	Messages        []Message      `json:"messages,omitempty"`
	Context         []Message      `json:"context,omitempty"`
	ContextSet      bool           `json:"context_set,omitempty"`
	Revision        uint64         `json:"-"`
	ContextRevision uint64         `json:"context_revision,omitempty"`
}

func (a *Agent) appendMessages(messages ...Message) error {
	return a.recordEvents(messageEvents(messages...)...)
}

func messageEvents(messages ...Message) []RuntimeEvent {
	events := make([]RuntimeEvent, 0, len(messages))
	for i := range messages {
		message := CloneMessages([]Message{messages[i]})[0]
		events = append(events, RuntimeEvent{Type: EventMessage, Message: &message})
	}
	return events
}

func (a *Agent) MessagesSnapshot() []Message {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.ensureStateLocked()
	return CloneMessages(a.Messages)
}

// requestMessages takes a cheap, shallow snapshot for synchronous request
// encoding. The loop is the only state writer and does not mutate messages
// while complete is consuming this slice.
func (a *Agent) requestMessages() []Message {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.ensureStateLocked()
	return append([]Message(nil), a.contextMessages...)
}

func (a *Agent) contextSnapshot() []Message {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.ensureStateLocked()
	return CloneMessages(a.contextMessages)
}

func (a *Agent) UsageSnapshot() Usage {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.Usage
}

func (a *Agent) StateSnapshot() State {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.ensureStateLocked()
	return State{
		Events:          cloneRuntimeEvents(a.Events),
		Messages:        CloneMessages(a.Messages),
		Context:         CloneMessages(a.contextMessages),
		ContextSet:      a.contextSet,
		Usage:           a.Usage,
		Revision:        a.Revision,
		ContextRevision: a.ContextRevision,
	}
}

// StateVersion returns retained-history metadata without cloning messages.
func (a *Agent) StateVersion() (messageCount int, revision uint64) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.ensureStateLocked()
	return len(a.Messages), a.Revision
}

// Restore replaces all runtime state from a snapshot. When Events is present,
// every projection is rebuilt from it and duplicated snapshot fields are
// ignored. Event-less state is treated as an in-memory legacy snapshot.
func (a *Agent) Restore(state State) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	a.Events = nil
	a.Messages = nil
	a.contextMessages = nil
	a.contextSet = false
	a.Usage = Usage{}
	a.Revision = state.Revision
	a.ContextRevision = 0
	a.runtimeIndex = runtimeEventIndex{}
	a.runtimeIndexSet = false

	if len(state.Events) > 0 {
		events := cloneRuntimeEvents(state.Events)
		index := newRuntimeEventIndex()
		if err := validateRuntimeBatch(events, index); err != nil {
			return err
		}
		for _, event := range events {
			index.observe(event)
			a.applyEventLocked(event)
		}
		a.runtimeIndex = index
		a.runtimeIndexSet = true
		return nil
	}

	a.Messages = CloneMessages(state.Messages)
	if state.ContextSet {
		a.contextMessages = CloneMessages(state.Context)
	} else {
		a.contextMessages = CloneMessages(state.Messages)
	}
	a.contextSet = true
	a.Usage = state.Usage
	a.ContextRevision = state.ContextRevision
	a.synthesizeEventsLocked()
	return nil
}

func (a *Agent) ensureStateLocked() {
	if len(a.Events) > 0 || (a.contextSet && len(a.Messages) == 0 && a.Usage == (Usage{})) {
		return
	}
	if !a.contextSet {
		a.contextMessages = CloneMessages(a.Messages)
		a.contextSet = true
	}
	a.synthesizeEventsLocked()
}

func (a *Agent) synthesizeEventsLocked() {
	if len(a.Events) > 0 {
		return
	}
	sequence := uint64(0)
	for i := range a.Messages {
		sequence++
		message := CloneMessages([]Message{a.Messages[i]})[0]
		a.Events = append(a.Events, RuntimeEvent{
			Sequence: sequence,
			ID:       fmt.Sprintf("legacy-message-%d", sequence),
			Type:     EventMessage,
			Message:  &message,
		})
	}
	if a.Usage != (Usage{}) {
		sequence++
		usage := a.Usage
		a.Events = append(a.Events, RuntimeEvent{
			Sequence: sequence,
			ID:       fmt.Sprintf("legacy-usage-%d", sequence),
			Type:     EventUsage,
			Usage:    &usage,
		})
	}
	if a.contextSet && !messagesEqual(a.contextMessages, a.Messages) {
		sequence++
		a.Events = append(a.Events, RuntimeEvent{
			Sequence:      sequence,
			ID:            fmt.Sprintf("legacy-context-%d", sequence),
			Type:          EventContextCheckpoint,
			Context:       CloneMessages(a.contextMessages),
			ContextReason: "legacy snapshot",
		})
	}
}

func messagesEqual(a, b []Message) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && string(left) == string(right)
}

// CloneMessages returns a deep-enough copy for handing message snapshots to
// callers while another goroutine may continue streaming into retained state.
func CloneMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = message
		out[i].Content = CloneContent(message.Content)
	}
	return out
}

// CloneContent returns an independent copy suitable for retaining after an API
// call. Content only contains value fields and one level of pointer fields.
func CloneContent(in []Content) []Content {
	out := make([]Content, len(in))
	for i, content := range in {
		out[i] = content
		if content.File != nil {
			file := *content.File
			out[i].File = &file
		}
		if content.Reasoning != nil {
			reasoning := *content.Reasoning
			out[i].Reasoning = &reasoning
		}
		if content.ToolCall != nil {
			call := *content.ToolCall
			if call.Presentation != nil {
				presentation := *call.Presentation
				presentation.Locations = append([]ToolLocation(nil), presentation.Locations...)
				call.Presentation = &presentation
			}
			out[i].ToolCall = &call
		}
		if content.ToolResult != nil {
			result := *content.ToolResult
			if result.Presentation != nil {
				presentation := *result.Presentation
				presentation.Locations = append([]ToolLocation(nil), presentation.Locations...)
				result.Presentation = &presentation
			}
			result.Metadata = maps.Clone(result.Metadata)
			out[i].ToolResult = &result
		}
	}
	return out
}

func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.Marshal(s)

	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("failed to write state: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("failed to write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("failed to write state: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("failed to write state: %w", err)
	}

	return nil
}

func (s *State) Load(path string) error {
	data, err := os.ReadFile(path)

	if err != nil {
		return fmt.Errorf("failed to read state: %w", err)
	}

	if err := json.Unmarshal(data, s); err != nil {
		return fmt.Errorf("failed to parse state: %w", err)
	}

	return nil
}
