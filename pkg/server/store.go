package server

import "sync"

type Store struct {
	mu      sync.RWMutex
	entries []ToolEntry
	nextID  int

	notify chan struct{}
}

func NewStore() *Store {
	return &Store{
		notify: make(chan struct{}, 1),
	}
}

func (s *Store) Notify() <-chan struct{} {
	return s.notify
}

func (s *Store) Add(entry ToolEntry) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	entry.ID = s.nextID

	s.entries = append(s.entries, entry)
	s.signal()

	return entry.ID
}

func (s *Store) Update(id int, fn func(*ToolEntry)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.entries {
		if s.entries[i].ID == id {
			fn(&s.entries[i])
			s.signal()
			return
		}
	}
}

func (s *Store) List() []ToolEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ToolEntry, len(s.entries))
	copy(result, s.entries)
	return result
}

func (s *Store) Get(id int) (ToolEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries {
		if e.ID == id {
			return e, true
		}
	}

	return ToolEntry{}, false
}

func (s *Store) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}
