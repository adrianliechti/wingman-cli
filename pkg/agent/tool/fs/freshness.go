package fs

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type fileState struct {
	target  fileTarget
	modTime time.Time
	size    int64
}

// Freshness tracks the on-disk state of files the session's file tools
// touched, so external modifications (user edits, linters, shell commands)
// can be detected and announced to the model. A nil Freshness disables it.
type Freshness struct {
	root *os.Root

	mu     sync.Mutex
	states map[string]fileState
}

func NewFreshness(root *os.Root) *Freshness {
	return &Freshness{root: root, states: map[string]fileState{}}
}

func (f *Freshness) keyFor(target fileTarget) string {
	if target.AbsPath != "" {
		return target.AbsPath
	}
	return filepath.Join(f.root.Name(), target.RelPath)
}

// record is skipped for background-agent tool calls: their modifications must
// stay visible as "changed on disk" to the main agent, and their reads must
// not add baseline entries the main agent never saw.
func (f *Freshness) record(ctx context.Context, target fileTarget) {
	if f == nil || tool.IsBackgroundOrigin(ctx) {
		return
	}
	info, err := statFileTarget(f.root, target)
	if err != nil || info.IsDir() {
		return
	}
	key := f.keyFor(target)
	f.mu.Lock()
	f.states[key] = fileState{target: target, modTime: info.ModTime(), size: info.Size()}
	f.mu.Unlock()
}

func (f *Freshness) forget(ctx context.Context, target fileTarget) {
	if f == nil || tool.IsBackgroundOrigin(ctx) {
		return
	}
	f.mu.Lock()
	delete(f.states, f.keyFor(target))
	f.mu.Unlock()
}

// stale reports whether the file's on-disk state no longer matches what the
// main agent's tools last saw — an external change it has not re-read yet.
func (f *Freshness) stale(ctx context.Context, target fileTarget, info os.FileInfo) bool {
	if f == nil || tool.IsBackgroundOrigin(ctx) {
		return false
	}
	key := f.keyFor(target)
	f.mu.Lock()
	st, ok := f.states[key]
	f.mu.Unlock()
	return ok && (!info.ModTime().Equal(st.modTime) || info.Size() != st.size)
}

// Changed stats every tracked file and returns the paths whose on-disk state
// no longer matches what the session's tools last saw. Each change reports
// once: the record is updated (or dropped, for deleted files) so repeated
// sweeps stay quiet until the next external modification.
func (f *Freshness) Changed() []string {
	if f == nil {
		return nil
	}

	f.mu.Lock()
	states := make(map[string]fileState, len(f.states))
	maps.Copy(states, f.states)
	f.mu.Unlock()

	var changed []string
	for key, st := range states {
		info, err := statFileTarget(f.root, st.target)

		f.mu.Lock()
		current, ok := f.states[key]
		if !ok || current.modTime != st.modTime || current.size != st.size {
			// A tool updated the record while we were sweeping — that change
			// is the session's own.
			f.mu.Unlock()
			continue
		}
		switch {
		case err != nil:
			delete(f.states, key)
			changed = append(changed, key+" (deleted)")
		case !info.ModTime().Equal(st.modTime) || info.Size() != st.size:
			f.states[key] = fileState{target: st.target, modTime: info.ModTime(), size: info.Size()}
			changed = append(changed, key)
		}
		f.mu.Unlock()
	}

	slices.Sort(changed)
	return changed
}
