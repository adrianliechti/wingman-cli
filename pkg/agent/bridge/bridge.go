package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/mcp"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverName = "bridge"

// Bridge provides IDE integration via a VS Code MCP bridge server.
type Bridge struct {
	manager *mcp.Manager
}

// Setup discovers a VS Code bridge from lockfiles and connects it to the MCP manager.
// Returns nil if no bridge is found.
func Setup(ctx context.Context, workingDir string, manager *mcp.Manager) *Bridge {
	url := discoverBridge(workingDir)
	if url == "" {
		return nil
	}

	if err := manager.AddServer(ctx, serverName, mcp.ServerConfig{URL: url}); err != nil {
		return nil
	}

	return &Bridge{manager: manager}
}

// IsConnected returns true if the bridge MCP server is connected.
func (b *Bridge) IsConnected() bool {
	if b == nil {
		return false
	}

	_, ok := b.manager.Sessions()[serverName]
	return ok
}

// GetIDEContext calls get_selection and formats it for prompt injection.
func (b *Bridge) GetIDEContext(ctx context.Context) string {
	if !b.IsConnected() {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	result, err := b.callTool(ctx, "get_selection", nil)
	if err != nil || result == "" || result == "No active selection" {
		return ""
	}

	var sel struct {
		File  string `json:"file"`
		Text  string `json:"text"`
		Start struct{ Line int } `json:"start"`
		End   struct{ Line int } `json:"end"`
	}

	if err := json.Unmarshal([]byte(result), &sel); err != nil {
		return ""
	}

	if sel.Text != "" {
		text := sel.Text
		if len(text) > 2000 {
			text = text[:2000] + "..."
		}

		return fmt.Sprintf("[The user selected lines %d to %d from %s in the IDE:\n%s\n\nThis may or may not be related to the current task.]",
			sel.Start.Line, sel.End.Line, sel.File, text)
	}

	if sel.File != "" {
		return fmt.Sprintf("[The user has %s open in the IDE. This may or may not be related to the current task.]", sel.File)
	}

	return ""
}

func (b *Bridge) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	session, ok := b.manager.Sessions()[serverName]
	if !ok {
		return "", fmt.Errorf("bridge not connected")
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return "", err
	}

	var parts []string
	for _, c := range result.Content {
		if text, ok := c.(*sdkmcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}

	return strings.Join(parts, "\n"), nil
}

// discoverBridge finds the best matching bridge lockfile for workingDir.
func discoverBridge(workingDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	lockDir := filepath.Join(home, ".wingman", "bridge")
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		return ""
	}

	var bestURL string
	var bestLen int

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(lockDir, entry.Name()))
		if err != nil {
			continue
		}

		var lf struct {
			URL        string   `json:"url"`
			Workspaces []string `json:"workspaces"`
		}
		if err := json.Unmarshal(data, &lf); err != nil || lf.URL == "" {
			continue
		}

		for _, folder := range lf.Workspaces {
			if isSubPath(folder, workingDir) && len(folder) > bestLen {
				bestLen = len(folder)
				bestURL = lf.URL
			}
		}
	}

	return bestURL
}

func isSubPath(parent, child string) bool {
	parent = filepath.Clean(parent) + string(filepath.Separator)
	child = filepath.Clean(child) + string(filepath.Separator)
	return strings.HasPrefix(child, parent)
}
