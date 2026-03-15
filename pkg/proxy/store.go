package proxy

import (
	"sync"

	"github.com/google/uuid"
)

type Store struct {
	mu      sync.RWMutex
	entries []RequestEntry
}

func NewStore() *Store {
	return &Store{}
}

func (s *Store) Add(entry RequestEntry) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry.ID = uuid.NewString()
	s.entries = append(s.entries, entry)

	return entry.ID
}

func (s *Store) List() []RequestEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]RequestEntry, len(s.entries))
	copy(result, s.entries)
	return result
}

func (s *Store) Get(id string) (RequestEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries {
		if e.ID == id {
			return e, true
		}
	}

	return RequestEntry{}, false
}

func (s *Store) TotalTokens() (input, output int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries {
		input += e.InputTokens
		output += e.OutputTokens
	}

	return
}
