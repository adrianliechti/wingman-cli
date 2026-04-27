package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type Settings struct {
	WingmanURL   string `json:"url"`
	WingmanToken string `json:"token"`
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
	if s.WingmanURL != "" {
		os.Setenv("WINGMAN_URL", s.WingmanURL)
	}

	if s.WingmanToken != "" {
		os.Setenv("WINGMAN_TOKEN", s.WingmanToken)
	}
}
