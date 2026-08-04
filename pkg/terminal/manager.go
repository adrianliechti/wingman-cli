package terminal

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

type Manager struct {
	dir string

	mu       sync.Mutex
	order    []string
	sessions map[string]*Session

	onExit func(id string)
}

func NewManager(dir string) *Manager {
	return &Manager{
		dir:      dir,
		sessions: map[string]*Session{},
	}
}

func (m *Manager) SetExitHandler(fn func(id string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onExit = fn
}

func (m *Manager) Create(shell string, cols, rows int) (*Session, error) {
	resolved, ok := resolveShell(shell)
	if !ok {
		return nil, fmt.Errorf("unknown shell %q", shell)
	}

	// Held across the whole creation so a shell that exits immediately cannot run
	// handleExit before the session is registered.
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := newSession(uuid.NewString(), resolved, m.dir, cols, rows, m.handleExit)

	if err != nil {
		return nil, err
	}

	m.sessions[s.ID()] = s
	m.order = append(m.order, s.ID())

	return s, nil
}

func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *Manager) List() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.order))
	for _, id := range m.order {
		if s, ok := m.sessions[id]; ok {
			out = append(out, s)
		}
	}
	return out
}

func (m *Manager) Remove(id string) bool {
	s := m.take(id)
	if s == nil {
		return false
	}
	_ = s.Close()
	return true
}

func (m *Manager) Close() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = map[string]*Session{}
	m.order = nil
	m.onExit = nil
	m.mu.Unlock()

	for _, s := range sessions {
		_ = s.Close()
	}
}

func (m *Manager) take(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil
	}
	delete(m.sessions, id)
	for i, sid := range m.order {
		if sid == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return s
}

func (m *Manager) handleExit(s *Session) {
	m.mu.Lock()
	fn := m.onExit
	m.mu.Unlock()

	m.take(s.ID())

	if fn != nil {
		fn(s.ID())
	}
}
