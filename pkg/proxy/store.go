package proxy

import "sync"

type Store struct {
	mu      sync.RWMutex
	entries []RequestEntry
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

func (s *Store) Add(entry RequestEntry) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	entry.ID = s.nextID

	s.entries = append(s.entries, entry)
	s.signal()

	return entry.ID
}

func (s *Store) Update(id int, fn func(*RequestEntry)) {
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

func (s *Store) List() []RequestEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]RequestEntry, len(s.entries))
	copy(result, s.entries)
	return result
}

func (s *Store) Get(id int) (RequestEntry, bool) {
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

func (s *Store) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}
