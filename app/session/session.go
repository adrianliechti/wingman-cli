package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	
)

// Session represents a saved conversation session.
type Session struct {
	ID        string      `json:"id"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	State     agent.State `json:"state"`
}

func Save(sessionsDir string, id string, state agent.State) (string, error) {
	dir := sessionsDir
	if dir == "" {
		return "", fmt.Errorf("no sessions directory available")
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create sessions directory: %w", err)
	}

	if len(state.Messages) == 0 {
		return "", nil
	}

	path := filepath.Join(dir, id+".json")

	now := time.Now()
	s := Session{
		ID:        id,
		UpdatedAt: now,
		State:     state,
	}

	if existing, err := loadFile(path); err == nil {
		s.CreatedAt = existing.CreatedAt
	} else {
		s.CreatedAt = now
	}

	data, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write session file: %w", err)
	}

	return id, nil
}

func Load(sessionsDir string, id string) (Session, error) {
	dir := sessionsDir
	if dir == "" {
		return Session{}, fmt.Errorf("no sessions directory available")
	}

	return loadFile(filepath.Join(dir, id+".json"))
}

func List(sessionsDir string) ([]Session, error) {
	dir := sessionsDir
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

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		s, err := loadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		s.State.Messages = nil
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func Delete(sessionsDir string, id string) error {
	dir := sessionsDir
	if dir == "" {
		return nil
	}

	return os.Remove(filepath.Join(dir, id+".json"))
}

func loadFile(path string) (Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, fmt.Errorf("failed to read session file: %w", err)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("failed to parse session file: %w", err)
	}

	if len(s.State.Messages) == 0 && s.State.Usage == (agent.Usage{}) {
		var legacy struct {
			Messages []agent.Message `json:"messages,omitempty"`
			Usage    agent.Usage     `json:"usage"`
		}

		if err := json.Unmarshal(data, &legacy); err == nil {
			s.State = agent.State{
				Messages: legacy.Messages,
				Usage:    legacy.Usage,
			}
		}
	}

	return s, nil
}
