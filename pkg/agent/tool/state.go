package tool

import (
	"os"
	"sync"
	"time"
)

type FileReadSnapshot struct {
	BaseDir string
	Path    string

	Content string
	ModTime time.Time
	Partial bool
}

type ReadTracker struct {
	mu    sync.RWMutex
	files map[string]FileReadSnapshot
}

func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		files: make(map[string]FileReadSnapshot),
	}
}

func (r *ReadTracker) Remember(root *os.Root, path, content string, modTime time.Time, partial bool) {
	if r == nil || root == nil {
		return
	}

	key := readTrackerKey(root.Name(), path)

	r.mu.Lock()
	r.files[key] = FileReadSnapshot{
		BaseDir: root.Name(),
		Path:    path,
		Content: content,
		ModTime: modTime,
		Partial: partial,
	}
	r.mu.Unlock()
}

func (r *ReadTracker) Snapshot(root *os.Root, path string) (FileReadSnapshot, bool) {
	if r == nil || root == nil {
		return FileReadSnapshot{}, false
	}

	key := readTrackerKey(root.Name(), path)

	r.mu.RLock()
	snapshot, ok := r.files[key]
	r.mu.RUnlock()

	return snapshot, ok
}

func (r *ReadTracker) Clear() {
	if r == nil {
		return
	}

	r.mu.Lock()
	r.files = make(map[string]FileReadSnapshot)
	r.mu.Unlock()
}

func readTrackerKey(baseDir, path string) string {
	return baseDir + "::" + path
}

type SessionState struct {
	mu sync.RWMutex

	planning bool
	planFile string
}

func NewSessionState() *SessionState {
	return &SessionState{}
}

func (s *SessionState) SetPlanMode(planFile string) {
	if s == nil {
		return
	}

	s.mu.Lock()
	s.planning = true
	s.planFile = planFile
	s.mu.Unlock()
}

func (s *SessionState) SetAgentMode() {
	if s == nil {
		return
	}

	s.mu.Lock()
	s.planning = false
	s.mu.Unlock()
}

func (s *SessionState) IsPlanning() bool {
	if s == nil {
		return false
	}

	s.mu.RLock()
	planning := s.planning
	s.mu.RUnlock()

	return planning
}

func (s *SessionState) PlanFile() string {
	if s == nil {
		return ""
	}

	s.mu.RLock()
	planFile := s.planFile
	s.mu.RUnlock()

	return planFile
}
