package tracker

import (
	"os"
	"sync"
	"time"
)

type Snapshot struct {
	Root string
	Path string

	Content string
	ModTime time.Time
	Partial bool
}

type Tracker struct {
	mu    sync.RWMutex
	files map[string]Snapshot
}

func New() *Tracker {
	return &Tracker{
		files: make(map[string]Snapshot),
	}
}

func (t *Tracker) Remember(root *os.Root, path, content string, modTime time.Time, partial bool) {
	if t == nil || root == nil {
		return
	}

	key := trackerKey(root.Name(), path)

	t.mu.Lock()
	t.files[key] = Snapshot{
		Root:    root.Name(),
		Path:    path,
		Content: content,
		ModTime: modTime,
		Partial: partial,
	}
	t.mu.Unlock()
}

func (t *Tracker) Get(root *os.Root, path string) (Snapshot, bool) {
	if t == nil || root == nil {
		return Snapshot{}, false
	}

	key := trackerKey(root.Name(), path)

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
