package session

// This file is the complete compatibility boundary for pre-ledger sessions.
// Remove it, the ensureCurrentFormat call sites, and the legacy List branches
// together when support for v1 JSON/JSONL sessions is retired.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

type legacyRecord struct {
	Type      string         `json:"type"`
	ID        string         `json:"id,omitempty"`
	CreatedAt *time.Time     `json:"created_at,omitempty"`
	Message   *agent.Message `json:"message,omitempty"`
	Usage     *agent.Usage   `json:"usage,omitempty"`
}

func ensureCurrentFormat(sessionsDir, id string) error {
	path := sessionPath(sessionsDir, id)
	if _, err := os.Stat(path); err == nil {
		current, detectErr := isCurrentJournal(path)
		if detectErr != nil {
			return fmt.Errorf("detect session journal format: %w", detectErr)
		}
		if current {
			return nil
		}
		legacy, loadErr := loadLegacyJSONL(path, id)
		if loadErr != nil {
			return loadErr
		}
		return migrateLegacySession(path, legacy)
	} else if !os.IsNotExist(err) {
		return err
	}

	legacyPath := filepath.Join(sessionsDir, id+".json")
	legacy, err := loadLegacyJSON(legacyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if legacy.ID == "" {
		legacy.ID = id
	}
	if err := migrateLegacySession(path, legacy); err != nil {
		return err
	}
	return os.Remove(legacyPath)
}

func loadLegacyJSONL(path, fallbackID string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, fmt.Errorf("failed to read legacy session file: %w", err)
	}
	defer f.Close()

	s := Session{ID: fallbackID}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 128*1024*1024)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var r legacyRecord
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return Session{}, fmt.Errorf("parse legacy session: %w", err)
		}
		switch r.Type {
		case "meta":
			if r.ID != "" {
				s.ID = r.ID
			}
			if r.CreatedAt != nil {
				s.CreatedAt = *r.CreatedAt
			}
		case "message":
			if r.Message != nil {
				s.State.Messages = append(s.State.Messages, *r.Message)
			}
		case "state":
			if r.Usage != nil {
				s.State.Usage = *r.Usage
			}
		default:
			return Session{}, fmt.Errorf("unknown legacy session record %q", r.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return Session{}, err
	}
	if s.CreatedAt.IsZero() && len(s.State.Messages) == 0 {
		return Session{}, fmt.Errorf("failed to parse legacy session file")
	}
	if stat, err := os.Stat(path); err == nil {
		s.UpdatedAt = stat.ModTime()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	s.Title = extractTitle(s.State.Messages)
	return s, nil
}

func loadLegacyJSON(path string) (Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, fmt.Errorf("failed to read legacy session file: %w", err)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("failed to parse legacy session file: %w", err)
	}
	if len(s.State.Messages) == 0 && len(s.State.Events) == 0 && s.State.Usage == (agent.Usage{}) {
		var flat struct {
			Messages []agent.Message `json:"messages,omitempty"`
			Usage    agent.Usage     `json:"usage"`
		}
		if err := json.Unmarshal(data, &flat); err == nil {
			s.State.Messages = flat.Messages
			s.State.Usage = flat.Usage
		}
	}
	if s.Title == "" {
		s.Title = extractTitle(s.State.Messages)
	}
	return s, nil
}

func migrateLegacySession(path string, legacy Session) error {
	state, err := normalizeState(legacy.State)
	if err != nil {
		return err
	}
	created := legacy.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	id := legacy.ID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".migrate-")
	if err != nil {
		return err
	}
	cleanup := func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}
	enc := json.NewEncoder(tmp)
	if err := enc.Encode(record{Type: "meta", Version: journalVersion, ID: id, CreatedAt: &created}); err != nil {
		cleanup()
		return err
	}
	for i := range state.Events {
		if err := enc.Encode(record{Type: "event", Event: &state.Events[i]}); err != nil {
			cleanup()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("migrate legacy session: %w", err)
	}
	return nil
}
