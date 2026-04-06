package tracker

import (
	"os"
	"sync"
	"time"
)

type Snapshot struct {
	Content string
	ModTime time.Time
	Partial bool
}

type Tracker struct {
	root *os.Root

	mu    sync.RWMutex
	files map[string]Snapshot
}

func New(root *os.Root) *Tracker {
	return &Tracker{
		root:  root,
		files: make(map[string]Snapshot),
	}
}

func (t *Tracker) Remember(path, content string, modTime time.Time, partial bool) {
	if t == nil || t.root == nil {
		return
	}

	key := trackerKey(t.root.Name(), path)

	t.mu.Lock()
	t.files[key] = Snapshot{
		Content: content,
		ModTime: modTime,
		Partial: partial,
	}
	t.mu.Unlock()
}

func (t *Tracker) Get(path string) (Snapshot, bool) {
	if t == nil || t.root == nil {
		return Snapshot{}, false
	}

	key := trackerKey(t.root.Name(), path)

	t.mu.RLock()
	snapshot, ok := t.files[key]
	t.mu.RUnlock()

	return snapshot, ok
}

func (t *Tracker) Clear() {
	if t == nil {
		return
	}

	t.mu.Lock()
	t.files = make(map[string]Snapshot)
	t.mu.Unlock()
}

func trackerKey(baseDir, path string) string {
	return baseDir + "::" + path
}
