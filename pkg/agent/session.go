package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/responses"
)

// Session represents a saved conversation session.
type Session struct {
	ID        string                                  `json:"id"`
	CreatedAt time.Time                               `json:"created_at"`
	UpdatedAt time.Time                               `json:"updated_at"`
	Messages  []responses.ResponseInputItemUnionParam `json:"-"`
	Usage     Usage                                   `json:"usage"`
}

// sessionFile is the on-disk representation, storing messages as raw JSON
// because ResponseInputItemUnionParam doesn't support standard json.Unmarshal.
type sessionFile struct {
	ID        string            `json:"id"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Messages  []json.RawMessage `json:"messages"`
	Usage     Usage             `json:"usage"`
}

// SaveSession persists the current messages to a session file.
// Creates the sessions directory if needed. Returns the session ID.
func (a *Agent) SaveSession(id string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	dir := a.sessionsDir()
	if dir == "" {
		return "", fmt.Errorf("no sessions directory available")
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create sessions directory: %w", err)
	}

	if len(a.messages) == 0 {
		return "", nil // nothing to save
	}

	path := filepath.Join(dir, id+".json")

	now := time.Now()

	// Marshal each message individually to raw JSON
	rawMsgs := make([]json.RawMessage, len(a.messages))
	for i, msg := range a.messages {
		b, err := json.Marshal(msg)
		if err != nil {
			return "", fmt.Errorf("failed to marshal message %d: %w", i, err)
		}
		rawMsgs[i] = b
	}

	sf := sessionFile{
		ID:        id,
		UpdatedAt: now,
		Messages:  rawMsgs,
		Usage:     a.usage,
	}

	// Preserve original creation time if file exists
	if existing, err := loadSessionFile(path); err == nil {
		sf.CreatedAt = existing.CreatedAt
	} else {
		sf.CreatedAt = now
	}

	data, err := json.Marshal(sf)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write session file: %w", err)
	}

	return id, nil
}

// LoadSession restores messages from a saved session file.
func (a *Agent) LoadSession(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	dir := a.sessionsDir()
	if dir == "" {
		return fmt.Errorf("no sessions directory available")
	}

	path := filepath.Join(dir, id+".json")

	session, err := loadSessionFile(path)
	if err != nil {
		return err
	}

	a.messages = session.Messages
	a.usage = session.Usage

	return nil
}

// ListSessions returns saved sessions, most recent first.
func (a *Agent) ListSessions() ([]Session, error) {
	dir := a.sessionsDir()
	if dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []Session

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, e.Name())

		s, err := loadSessionFile(path)
		if err != nil {
			continue
		}

		// Don't load full messages into listing
		s.Messages = nil
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

// DeleteSession removes a saved session file.
func (a *Agent) DeleteSession(id string) error {
	dir := a.sessionsDir()
	if dir == "" {
		return nil
	}

	return os.Remove(filepath.Join(dir, id+".json"))
}

func (a *Agent) sessionsDir() string {
	if a.Environment == nil || a.Environment.Memory == nil {
		return ""
	}

	// Sessions live next to memory: ~/.wingman/projects/<project>/sessions/
	memoryDir := a.Environment.MemoryDir()
	return filepath.Join(filepath.Dir(memoryDir), "sessions")
}

func loadSessionFile(path string) (Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, fmt.Errorf("failed to read session file: %w", err)
	}

	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return Session{}, fmt.Errorf("failed to parse session file: %w", err)
	}

	// Deserialize each message via ResponseInputItemUnion (which has proper
	// UnmarshalJSON), then convert to the Param type using the same
	// RawJSON→Unmarshal pattern the streaming code uses in agent.go.
	msgs := make([]responses.ResponseInputItemUnionParam, 0, len(sf.Messages))
	for _, raw := range sf.Messages {
		var item responses.ResponseInputItemUnion
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		msgs = append(msgs, inputItemToParam(item))
	}

	return Session{
		ID:        sf.ID,
		CreatedAt: sf.CreatedAt,
		UpdatedAt: sf.UpdatedAt,
		Messages:  msgs,
		Usage:     sf.Usage,
	}, nil
}

// inputItemToParam converts a deserialized ResponseInputItemUnion to a
// ResponseInputItemUnionParam with the correct Of* field populated.
// This mirrors how streamResponse in agent.go converts streaming output items.
func inputItemToParam(item responses.ResponseInputItemUnion) responses.ResponseInputItemUnionParam {
	raw := json.RawMessage(item.RawJSON())

	switch item.Type {
	case "message":
		if item.Role == "assistant" {
			var p responses.ResponseOutputMessageParam
			if json.Unmarshal(raw, &p) == nil {
				return responses.ResponseInputItemUnionParam{OfOutputMessage: &p}
			}
		}
		var p responses.EasyInputMessageParam
		if json.Unmarshal(raw, &p) == nil {
			return responses.ResponseInputItemUnionParam{OfMessage: &p}
		}
	case "function_call":
		var p responses.ResponseFunctionToolCallParam
		if json.Unmarshal(raw, &p) == nil {
			return responses.ResponseInputItemUnionParam{OfFunctionCall: &p}
		}
	case "function_call_output":
		var p responses.ResponseInputItemFunctionCallOutputParam
		if json.Unmarshal(raw, &p) == nil {
			return responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &p}
		}
	case "reasoning":
		var p responses.ResponseReasoningItemParam
		if json.Unmarshal(raw, &p) == nil {
			return responses.ResponseInputItemUnionParam{OfReasoning: &p}
		}
	case "compaction":
		var p responses.ResponseCompactionItemParam
		if json.Unmarshal(raw, &p) == nil {
			return responses.ResponseInputItemUnionParam{OfCompaction: &p}
		}
	default:
		// No type field — EasyInputMessage (user messages saved without "type")
		if item.Role != "" {
			var p responses.EasyInputMessageParam
			if json.Unmarshal(raw, &p) == nil {
				return responses.ResponseInputItemUnionParam{OfMessage: &p}
			}
		}
	}

	// Fallback: use param.Override to preserve the raw JSON for the API
	return item.ToParam()
}
