package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/env/tracker"
)

type Environment struct {
	OS   string
	Arch string

	Root    *os.Root
	Memory  *os.Root
	Scratch *os.Root

	Tracker *tracker.Tracker

	planFile string

	AskUser      func(question string) (string, error)
	PromptUser   func(prompt string) (bool, error)
	DiagnoseFile func(ctx context.Context, path string) string
	StatusUpdate func(status string)
}

// New creates an Environment rooted at the given working directory.
// It opens the workspace root, creates a scratch directory, initializes
// memory, and sets up the tracker.
func New(workingDir string) (*Environment, error) {
	root, err := os.OpenRoot(workingDir)

	if err != nil {
		return nil, fmt.Errorf("failed to open workspace root: %w", err)
	}

	scratchDir := filepath.Join(os.TempDir(), fmt.Sprintf("wingman-%d", time.Now().Unix()))

	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		root.Close()
		return nil, fmt.Errorf("failed to create scratch directory: %w", err)
	}

	scratch, err := os.OpenRoot(scratchDir)

	if err != nil {
		root.Close()
		return nil, fmt.Errorf("failed to open scratch directory: %w", err)
	}

	e := &Environment{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,

		Root:    root,
		Scratch: scratch,

		Tracker: tracker.New(root),
	}

	memoryDir := projectMemoryDir(workingDir)

	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		scratch.Close()
		os.RemoveAll(scratchDir)
		root.Close()
		return nil, fmt.Errorf("failed to create memory directory: %w", err)
	}

	memoryRoot, err := os.OpenRoot(memoryDir)

	if err != nil {
		scratch.Close()
		os.RemoveAll(scratchDir)
		root.Close()
		return nil, fmt.Errorf("failed to open memory directory: %w", err)
	}

	e.Memory = memoryRoot

	return e, nil
}

// Close releases all resources held by the environment.
func (e *Environment) Close() {
	if e.Memory != nil {
		e.Memory.Close()
	}

	if e.Scratch != nil {
		scratchDir := e.Scratch.Name()
		e.Scratch.Close()
		os.RemoveAll(scratchDir)
	}

	if e.Root != nil {
		e.Root.Close()
	}
}

func (e *Environment) RootDir() string {
	return e.Root.Name()
}

func (e *Environment) MemoryDir() string {
	return e.Memory.Name()
}

func (e *Environment) ScratchDir() string {
	return e.Scratch.Name()
}

func projectMemoryDir(workingDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	sanitized := filepath.Clean(workingDir)

	// On Windows, strip the drive letter prefix (e.g. "C:" or "F:") and the
	// following separator so the sanitized name doesn't contain a colon, which
	// is an invalid character in Windows file/folder names.
	if vol := filepath.VolumeName(sanitized); vol != "" {
		sanitized = strings.TrimPrefix(sanitized, vol)
	}

	sanitized = strings.TrimPrefix(sanitized, string(filepath.Separator))
	sanitized = strings.ReplaceAll(sanitized, string(filepath.Separator), "_")
	sanitized = strings.ToLower(sanitized)

	return filepath.Join(home, ".wingman", "projects", sanitized, "memory")
}
