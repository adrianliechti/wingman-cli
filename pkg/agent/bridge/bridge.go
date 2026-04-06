package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	mgr "github.com/adrianliechti/wingman-agent/pkg/agent/mcp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName        = "bridge"
	workspaceStateURI = "wingman://workspace/state"
)

type workspaceState struct {
	ActiveFile string     `json:"activeFile"`
	OpenFiles  []string   `json:"openFiles"`
	Selection  *selection `json:"selection,omitempty"`
}

type selection struct {
	FilePath string   `json:"filePath"`
	Text     string   `json:"text"`
	Start    position `json:"start"`
	End      position `json:"end"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Bridge provides IDE integration via a VS Code MCP bridge server.
type Bridge struct {
	session *mcp.ClientSession

	mu    sync.RWMutex
	state workspaceState
}

// Setup discovers a VS Code bridge from lockfiles and connects it to the MCP manager.
// Returns nil if no bridge is found.
func Setup(ctx context.Context, workingDir string, manager *mgr.Manager) *Bridge {
	url := discoverBridge(workingDir)
	if url == "" {
		return nil
	}

	b := &Bridge{}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(ctx context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			if req.Params != nil && req.Params.URI == workspaceStateURI {
				b.refreshState(ctx)
			}
		},
	})

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	session, err := client.Connect(connectCtx, &mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		return nil
	}

	b.session = session
	manager.AddSession(serverName, session)

	// Subscribe to workspace state changes and read initial state
	_ = session.Subscribe(ctx, &mcp.SubscribeParams{URI: workspaceStateURI})
	b.refreshState(ctx)

	return b
}

// IsConnected returns true if the bridge MCP server is connected.
func (b *Bridge) IsConnected() bool {
	return b != nil && b.session != nil
}

// Close shuts down the bridge session.
func (b *Bridge) Close() {
	if b != nil && b.session != nil {
		b.session.Close()
		b.session = nil
	}
}

// GetInstructions returns the MCP server instructions from the bridge.
func (b *Bridge) GetInstructions() string {
	if !b.IsConnected() {
		return ""
	}

	if result := b.session.InitializeResult(); result != nil {
		return result.Instructions
	}

	return ""
}

// GetContext returns the current IDE state formatted for prompt injection.
func (b *Bridge) GetContext() string {
	if !b.IsConnected() {
		return ""
	}

	b.mu.RLock()
	state := b.state
	b.mu.RUnlock()

	var parts []string

	if state.Selection != nil && state.Selection.Text != "" {
		text := state.Selection.Text
		if len(text) > 2000 {
			text = text[:2000] + "..."
		}

		parts = append(parts, fmt.Sprintf("The user selected lines %d to %d from %s in the IDE:\n%s",
			state.Selection.Start.Line+1, state.Selection.End.Line+1, state.Selection.FilePath, text))
	} else if state.ActiveFile != "" {
		parts = append(parts, fmt.Sprintf("The user has %s open in the IDE.", state.ActiveFile))
	}

	if len(state.OpenFiles) > 0 {
		parts = append(parts, fmt.Sprintf("Open tabs: %s", strings.Join(state.OpenFiles, ", ")))
	}

	if len(parts) == 0 {
		return ""
	}

	return fmt.Sprintf("[%s\n\nThis may or may not be related to the current task.]", strings.Join(parts, "\n"))
}

// GetDiagnostics fetches LSP diagnostics from the IDE for a file.
func (b *Bridge) GetDiagnostics(ctx context.Context, path string) (string, error) {
	if !b.IsConnected() {
		return "", fmt.Errorf("bridge not connected")
	}

	args := map[string]any{}
	if path != "" {
		args["path"] = path
	}

	return b.callTool(ctx, "get_lsp_diagnostics", args)
}

// NotifyFileUpdated tells the IDE that a file was changed externally
// so language services re-analyze it.
func (b *Bridge) NotifyFileUpdated(ctx context.Context, path string) {
	if !b.IsConnected() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	b.callTool(ctx, "notify_file_updated", map[string]any{"path": path})
}

func (b *Bridge) refreshState(ctx context.Context) {
	if b.session == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	result, err := b.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: workspaceStateURI})
	if err != nil || len(result.Contents) == 0 || result.Contents[0].Text == "" {
		return
	}

	var state workspaceState
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &state); err != nil {
		return
	}

	b.mu.Lock()
	b.state = state
	b.mu.Unlock()
}

func (b *Bridge) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	result, err := b.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return "", err
	}

	var parts []string
	for _, c := range result.Content {
		if text, ok := c.(*mcp.TextContent); ok {
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
			PID        int      `json:"pid"`
			Workspaces []string `json:"workspaces"`
		}
		if err := json.Unmarshal(data, &lf); err != nil || lf.URL == "" {
			continue
		}

		if !isProcessAlive(lf.PID) {
			os.Remove(filepath.Join(lockDir, entry.Name()))
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

func isProcessAlive(pid int) bool {
	if pid == 0 {
		return true
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	return p.Signal(syscall.Signal(0)) == nil
}
