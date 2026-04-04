package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type bridgeLockfile struct {
	URL        string   `json:"url"`
	PID        int      `json:"pid"`
	Workspaces []string `json:"workspaces"`
}

// DiscoverBridge scans ~/.wingman/bridge/ for lockfiles and returns an MCP
// server config for the bridge whose workspace contains workingDir.
// Returns nil if no matching bridge is found.
func DiscoverBridge(workingDir string) *ServerConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	lockDir := filepath.Join(home, ".wingman", "bridge")

	entries, err := os.ReadDir(lockDir)
	if err != nil {
		return nil
	}

	var bestURL string
	var bestLen int

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}

		path := filepath.Join(lockDir, entry.Name())

		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var lf bridgeLockfile
		if err := json.Unmarshal(data, &lf); err != nil {
			continue
		}

		for _, folder := range lf.Workspaces {
			if !isSubPath(folder, workingDir) {
				continue
			}
			if len(folder) > bestLen {
				bestLen = len(folder)
				bestURL = lf.URL
			}
		}
	}

	if bestURL == "" {
		return nil
	}

	return &ServerConfig{URL: bestURL}
}

func isSubPath(parent, child string) bool {
	parent = filepath.Clean(parent) + string(filepath.Separator)
	child = filepath.Clean(child) + string(filepath.Separator)
	return strings.HasPrefix(child, parent)
}
