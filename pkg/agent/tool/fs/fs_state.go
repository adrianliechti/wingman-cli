package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env"
)

func rememberRead(env *env.Environment, path string, content []byte, offset, limit int) {
	var modTime time.Time

	if info, err := env.Root.Stat(path); err == nil {
		modTime = info.ModTime()
	}

	env.Tracker.Remember(path, string(content), modTime, offset, limit)
}

func requireFreshFullRead(env *env.Environment, path, content string) error {
	snapshot, ok := env.Tracker.Get(path)

	if !ok {
		return fmt.Errorf("file has not been read yet. Read it first before writing to it")
	}

	if snapshot.IsPartial() {
		return fmt.Errorf("file was only partially read. Read the full file before writing to it")
	}

	if content != snapshot.Content {
		return fmt.Errorf("file has been modified since it was read. Read it again before writing to it")
	}

	return nil
}

func enforcePlanMutation(env *env.Environment, path string) error {
	if !env.IsPlanning() {
		return nil
	}

	planFile := env.PlanFile()

	if planFile == "" {
		return fmt.Errorf("plan mode is active, but no session plan file is configured")
	}

	target := absoluteRootPath(env.Root, path)

	if !samePath(target, planFile) {
		return fmt.Errorf("plan mode is read-only. You may only modify the session plan file %s", planFile)
	}

	return nil
}

func absoluteRootPath(root *os.Root, path string) string {
	if root == nil {
		return path
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	if path == "." {
		return filepath.Clean(root.Name())
	}

	return filepath.Clean(filepath.Join(root.Name(), path))
}

func samePath(a, b string) bool {
	return normalizePathForComparison(filepath.Clean(a)) == normalizePathForComparison(filepath.Clean(b))
}
