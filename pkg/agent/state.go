package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type State struct {
	Usage    Usage     `json:"usage"`
	Messages []Message `json:"messages,omitempty"`
	Revision uint64    `json:"-"`
}

func (a *Agent) appendMessages(messages ...Message) {
	if len(messages) == 0 {
		return
	}
	a.stateMu.Lock()
	a.Messages = append(a.Messages, messages...)
	a.stateMu.Unlock()
}

func (a *Agent) MessagesSnapshot() []Message {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return CloneMessages(a.Messages)
}

// requestMessages takes a cheap, shallow snapshot for synchronous request
// encoding. The loop is the only state writer and does not mutate messages
// while complete is consuming this slice.
func (a *Agent) requestMessages() []Message {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return append([]Message(nil), a.Messages...)
}

func (a *Agent) UsageSnapshot() Usage {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.Usage
}

func (a *Agent) StateSnapshot() State {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return State{
		Messages: CloneMessages(a.Messages),
		Usage:    a.Usage,
		Revision: a.Revision,
	}
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
			out[i].ToolCall = &call
		}
		if content.ToolResult != nil {
			result := *content.ToolResult
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
