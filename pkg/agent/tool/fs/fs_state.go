package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func rememberRead(env *tool.Environment, root *os.Root, normalizedPath string, content []byte, partial bool) {
	if env == nil || env.Tracker == nil || root == nil {
		return
	}

	var modTime time.Time
	if info, err := root.Stat(normalizedPath); err == nil {
		modTime = info.ModTime()
	}

	env.Tracker.Remember(root, normalizedPath, string(content), modTime, partial)
}

func requireFreshFullRead(env *tool.Environment, root *os.Root, normalizedPath, currentContent string) error {
	if env == nil || env.Tracker == nil || root == nil {
		return nil
	}

	snapshot, ok := env.Tracker.Get(root, normalizedPath)
	if !ok {
		return fmt.Errorf("file has not been read yet. Read it first before writing to it")
	}

	if snapshot.Partial {
		return fmt.Errorf("file was only partially read. Read the full file before writing to it")
	}

	info, err := root.Stat(normalizedPath)
	if err == nil && !snapshot.ModTime.IsZero() && info.ModTime().After(snapshot.ModTime) && currentContent != snapshot.Content {
		return fmt.Errorf("file has been modified since it was read. Read it again before writing to it")
	}

	return nil
}

func enforcePlanMutation(env *tool.Environment, root *os.Root, normalizedPath string) error {
	if env == nil || !env.IsPlanning() {
		return nil
	}

	planFile := env.PlanFile()
	if planFile == "" {
		return fmt.Errorf("plan mode is active, but no session plan file is configured")
	}

	target := absoluteRootPath(root, normalizedPath)
	if !samePath(target, planFile) {
		return fmt.Errorf("plan mode is read-only. You may only modify the session plan file %s", planFile)
	}

	return nil
}

func absoluteRootPath(root *os.Root, normalizedPath string) string {
	if root == nil {
		return normalizedPath
	}

	if filepath.IsAbs(normalizedPath) {
		return filepath.Clean(normalizedPath)
	}

	if normalizedPath == "." {
		return filepath.Clean(root.Name())
	}

	return filepath.Clean(filepath.Join(root.Name(), normalizedPath))
}

func samePath(a, b string) bool {
	return normalizePathForComparison(filepath.Clean(a)) == normalizePathForComparison(filepath.Clean(b))
}
