package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxWorkspaces = 3
const maxRemoteWorkspaces = 8

const remoteKindSSH = "ssh"

type RemoteWorkspace struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Host string `json:"host"`
	Path string `json:"path"`
}

func (r RemoteWorkspace) normalized() RemoteWorkspace {
	r.Kind = strings.ToLower(strings.TrimSpace(r.Kind))
	if r.Kind == "" {
		r.Kind = remoteKindSSH
	}
	r.Name = strings.TrimSpace(r.Name)
	r.Host = strings.TrimSpace(r.Host)
	r.Path = strings.TrimSpace(r.Path)
	return r
}

func (r RemoteWorkspace) key() string {
	r = r.normalized()
	return r.Kind + "://" + r.Host + "/" + strings.TrimPrefix(r.Path, "/")
}

func (r RemoteWorkspace) displayName() string {
	if name := strings.TrimSpace(r.Name); name != "" {
		return name
	}
	if name := path.Base(strings.TrimRight(r.Path, "/")); name != "." && name != "/" && name != "" {
		return name
	}
	return r.Host
}

type Settings struct {
	WingmanURL   string            `json:"url"`
	WingmanToken string            `json:"token"`
	LargeContext bool              `json:"large_context,omitempty"`
	Workspaces   []string          `json:"workspaces,omitempty"`
	Remotes      []RemoteWorkspace `json:"remotes,omitempty"`
}

func (s *Settings) AddWorkspace(path string) {
	if path == "" {
		return
	}

	filtered := make([]string, 0, len(s.Workspaces)+1)
	filtered = append(filtered, path)
	for _, p := range s.Workspaces {
		if p == path {
			continue
		}
		filtered = append(filtered, p)
	}

	if len(filtered) > maxWorkspaces {
		filtered = filtered[:maxWorkspaces]
	}

	s.Workspaces = filtered
}

func (s *Settings) RemoveWorkspace(path string) {
	filtered := make([]string, 0, len(s.Workspaces))
	for _, p := range s.Workspaces {
		if p == path {
			continue
		}
		filtered = append(filtered, p)
	}

	s.Workspaces = filtered
}

func (s *Settings) AddRemote(remote RemoteWorkspace) {
	remote = remote.normalized()
	if remote.Host == "" || remote.Path == "" {
		return
	}

	key := remote.key()
	filtered := make([]RemoteWorkspace, 0, len(s.Remotes)+1)
	filtered = append(filtered, remote)
	for _, existing := range s.Remotes {
		if existing.key() == key {
			continue
		}
		filtered = append(filtered, existing.normalized())
	}

	if len(filtered) > maxRemoteWorkspaces {
		filtered = filtered[:maxRemoteWorkspaces]
	}

	s.Remotes = filtered
}

func (s *Settings) RemoveRemote(key string) {
	filtered := make([]RemoteWorkspace, 0, len(s.Remotes))
	for _, remote := range s.Remotes {
		if remote.key() == key {
			continue
		}
		filtered = append(filtered, remote)
	}
	s.Remotes = filtered
}

func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".wingman", "config.json"), nil
}

func loadSettings() (Settings, error) {
	var s Settings

	path, err := settingsPath()
	if err != nil {
		return s, err
	}

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return s, err
	}

	if len(data) > 0 {
		if err := json.Unmarshal(data, &s); err != nil {
			return s, err
		}
	}

	if s.WingmanURL == "" {
		s.WingmanURL = os.Getenv("WINGMAN_URL")
	}

	if s.WingmanToken == "" {
		s.WingmanToken = os.Getenv("WINGMAN_TOKEN")
	}

	return s, nil
}

func saveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

func (s Settings) Apply() {
	os.Setenv("WINGMAN_URL", s.WingmanURL)
	os.Setenv("WINGMAN_TOKEN", s.WingmanToken)

	if s.LargeContext {
		os.Setenv("WINGMAN_LARGE_CONTEXT", "1")
	}
}
