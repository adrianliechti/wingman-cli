package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// skipDirs lists directory names that are never worth watching — they tend to
// generate huge volumes of events (build outputs, vendor trees, VCS metadata)
// that would only debounce-spam the websocket without ever surfacing meaningful
// user changes.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".cache":       true,
	".venv":        true,
	"__pycache__":  true,
}

// watchWorkdir starts a recursive fsnotify watcher rooted at root. On any
// debounced batch of events it invokes onChange. Returns when ctx is cancelled.
func (s *Server) watchWorkdir(ctx context.Context, root string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	addDir := func(path string) {
		_ = w.Add(path)
	}

	walkAndWatch := func(base string) {
		filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if path != base && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			addDir(path)
			return nil
		})
	}

	walkAndWatch(root)

	const debounce = 300 * time.Millisecond
	var timer *time.Timer
	fire := func() {
		onChange()
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}

			// If a new directory was created, start watching it too.
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					name := filepath.Base(ev.Name)
					if !skipDirs[name] && !strings.HasPrefix(name, ".") {
						walkAndWatch(ev.Name)
					}
				}
			}

			if timer == nil {
				timer = time.AfterFunc(debounce, fire)
			} else {
				timer.Reset(debounce)
			}

		case _, ok := <-w.Errors:
			if !ok {
				return nil
			}
		}
	}
}
