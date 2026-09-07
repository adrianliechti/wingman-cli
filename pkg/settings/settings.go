// Package settings stores user preferences and desktop launcher state.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

const MaxWorkspaces = 3

type WindowTerminalPosition string

const (
	WindowTerminalPositionTab    WindowTerminalPosition = "tab"
	WindowTerminalPositionBottom WindowTerminalPosition = "bottom"
)

func (position WindowTerminalPosition) Valid() bool {
	return position == WindowTerminalPositionTab || position == WindowTerminalPositionBottom
}

type Settings struct {
	EditorTabCompletion    bool                   `json:"editor.tab.completion"`
	WindowTerminalPosition WindowTerminalPosition `json:"window.terminal.position"`
	Workspaces             []string               `json:"workspaces,omitempty"`
}

func (s *Settings) AddWorkspace(path string) {
	if path == "" {
		return
	}

	filtered := make([]string, 0, len(s.Workspaces)+1)
	filtered = append(filtered, path)
	for _, existing := range s.Workspaces {
		if existing != path {
			filtered = append(filtered, existing)
		}
	}
	if len(filtered) > MaxWorkspaces {
		filtered = filtered[:MaxWorkspaces]
	}
	s.Workspaces = filtered
}

func (s *Settings) RemoveWorkspace(path string) {
	filtered := make([]string, 0, len(s.Workspaces))
	for _, existing := range s.Workspaces {
		if existing != path {
			filtered = append(filtered, existing)
		}
	}
	s.Workspaces = filtered
}

var fileMu sync.Mutex

func path() (string, error) {
	return layout.WingmanPath("config.json")
}

func Load() (Settings, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	return load()
}

// Update serializes launcher read-modify-write operations within the process.
func Update(update func(*Settings)) (Settings, error) {
	fileMu.Lock()
	defer fileMu.Unlock()

	value, err := load()
	if err != nil {
		return Settings{}, err
	}
	update(&value)
	if err := save(value); err != nil {
		return Settings{}, err
	}
	return value, nil
}

func load() (Settings, error) {
	value := Settings{
		EditorTabCompletion:    true,
		WindowTerminalPosition: WindowTerminalPositionTab,
	}
	path, err := path()
	if err != nil {
		return Settings{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return Settings{}, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return Settings{}, err
	}
	if !value.WindowTerminalPosition.Valid() {
		value.WindowTerminalPosition = WindowTerminalPositionTab
	}
	return value, nil
}

func save(value Settings) error {
	path, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
