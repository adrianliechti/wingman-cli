package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxRestarts = 3

// Manager caches LSP sessions so servers are reused across tool invocations.
type Manager struct {
	workingDir string
	sessions   map[string]*Session // keyed by server command
	restarts   map[string]int      // restart count per server command
	mu         sync.Mutex

	detection detectionResult // cached detection results
	detectOnce sync.Once
}

// NewManager creates a new LSP session manager.
func NewManager(workingDir string) *Manager {
	return &Manager{
		workingDir: workingDir,
		sessions:   make(map[string]*Session),
		restarts:   make(map[string]int),
	}
}

// WorkingDir returns the working directory for this manager.
func (m *Manager) WorkingDir() string {
	return m.workingDir
}

// detect returns cached detection results, running detection once.
func (m *Manager) detect() *detectionResult {
	m.detectOnce.Do(func() {
		m.detection = detectAll(m.workingDir)
	})
	return &m.detection
}

// MissingServers returns project types detected in the workspace
// that have no available LSP server binary.
func (m *Manager) MissingServers() []MissingServer {
	return m.detect().Missing
}

// FindServer finds an appropriate LSP server for the given file.
func (m *Manager) FindServer(filePath string) *Server {
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	if ext == "" {
		return nil
	}

	dir := filepath.Dir(filePath)
	roots := m.detect().Roots

	var best *Server
	bestLen := -1

	for _, root := range roots {
		if !isSubPath(root.Dir, dir) {
			continue
		}
		if len(root.Dir) <= bestLen {
			continue
		}
		for _, s := range root.Servers {
			if hasLanguage(s.Languages, ext) {
				srv := s
				best = &srv
				bestLen = len(root.Dir)
				break
			}
		}
	}

	return best
}

// DetectServers finds all available LSP servers for the workspace.
func (m *Manager) DetectServers() []Server {
	roots := m.detect().Roots

	var servers []Server
	seen := make(map[string]bool)

	for _, root := range roots {
		for _, s := range root.Servers {
			if seen[s.Command] {
				continue
			}
			seen[s.Command] = true
			servers = append(servers, s)
		}
	}

	return servers
}

// GetSession returns a cached session or creates a new one for the given file.
func (m *Manager) GetSession(ctx context.Context, filePath string) (*Session, error) {
	server := m.FindServer(filePath)
	if server == nil {
		return nil, fmt.Errorf("no LSP server found for file: %s", filePath)
	}

	return m.GetSessionByServer(ctx, *server)
}

// GetSessionByServer returns a cached session or creates a new one for a specific server.
// If the server has crashed, it attempts to restart it and re-open previously opened documents.
func (m *Manager) GetSessionByServer(ctx context.Context, server Server) (*Session, error) {
	key := server.Command

	// Fast path: check cache
	m.mu.Lock()
	if session, ok := m.sessions[key]; ok {
		if session.IsAlive() {
			m.mu.Unlock()
			return session, nil
		}

		// Server crashed — collect state for recovery
		openedURIs := session.OpenedDocURIs()
		restartCount := m.restarts[key]
		delete(m.sessions, key)
		m.mu.Unlock()

		if restartCount >= maxRestarts {
			return nil, fmt.Errorf("LSP server %s crashed %d times, not restarting", server.Name, restartCount)
		}

		// Attempt restart
		newSession, err := connect(ctx, m.workingDir, server)
		if err != nil {
			return nil, fmt.Errorf("restart %s: %w", server.Name, err)
		}

		m.mu.Lock()
		// Check for concurrent restart race
		if existing, ok := m.sessions[key]; ok && existing.IsAlive() {
			m.mu.Unlock()
			newSession.Close()
			return existing, nil
		}
		m.sessions[key] = newSession
		m.restarts[key] = restartCount + 1
		m.mu.Unlock()

		// Re-open previously opened documents (best-effort)
		for _, uri := range openedURIs {
			path := uriToPath(uri)
			if path != "" {
				newSession.OpenDocument(ctx, path)
			}
		}

		return newSession, nil
	}
	m.mu.Unlock()

	// Slow path: first connection
	session, err := connect(ctx, m.workingDir, server)
	if err != nil {
		return nil, err
	}

	// Store result, handle concurrent connection race
	m.mu.Lock()
	if existing, ok := m.sessions[key]; ok && existing.IsAlive() {
		m.mu.Unlock()
		session.Close()
		return existing, nil
	}
	m.sessions[key] = session
	m.mu.Unlock()

	return session, nil
}

// Close shuts down all cached sessions.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, session := range m.sessions {
		session.Close()
		delete(m.sessions, key)
	}
}

// WorkspaceDiagnostics collects diagnostics across all workspace files.
func (m *Manager) WorkspaceDiagnostics(ctx context.Context) (string, error) {
	servers := m.DetectServers()
	if len(servers) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	var sb strings.Builder
	totalDiags := 0

	for _, server := range servers {
		session, err := m.GetSessionByServer(ctx, server)
		if err != nil {
			continue
		}

		for _, file := range discoverSourceFiles(m.workingDir, server.Languages, 50) {
			uri, err := session.OpenDocument(ctx, file)
			if err != nil {
				continue
			}

			diags := session.CollectDiagnostics(ctx, uri)
			if len(diags) == 0 {
				continue
			}

			displayPath := relPath(m.workingDir, file)
			for _, diag := range diags {
				totalDiags++
				fmt.Fprintf(&sb, "  %s:%d:%d %s: %s\n", displayPath, diag.Range.Start.Line+1, diag.Range.Start.Character+1, DiagnosticSeverityName(diag.Severity), diag.Message)
			}
		}
	}

	if totalDiags == 0 {
		return "No workspace diagnostics found", nil
	}

	return fmt.Sprintf("Workspace Diagnostics (%d found):\n%s", totalDiags, sb.String()), nil
}

// WorkspaceSymbols searches for symbols across the workspace.
func (m *Manager) WorkspaceSymbols(ctx context.Context, query string) (string, error) {
	servers := m.DetectServers()
	if len(servers) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	var allSymInfos []SymbolInformation
	var allWsSymbols []WorkspaceSymbol

	for _, server := range servers {
		session, err := m.GetSessionByServer(ctx, server)
		if err != nil {
			continue
		}

		var result json.RawMessage
		if err := session.CallAndAwait(ctx, "workspace/symbol", WorkspaceSymbolParams{Query: query}, &result); err != nil || result == nil || string(result) == "null" {
			continue
		}

		// Try SymbolInformation[] first (has location.uri with range)
		var symInfos []SymbolInformation
		if err := unmarshalResult(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
			allSymInfos = append(allSymInfos, symInfos...)
			continue
		}

		// Fall back to WorkspaceSymbol[] (location range may be omitted)
		var wsSymbols []WorkspaceSymbol
		if err := unmarshalResult(result, &wsSymbols); err == nil {
			allWsSymbols = append(allWsSymbols, wsSymbols...)
		}
	}

	if len(allSymInfos) > 0 {
		return formatSymbolInformations(allSymInfos, m.workingDir), nil
	}

	if len(allWsSymbols) > 0 {
		return formatWorkspaceSymbols(allWsSymbols, m.workingDir), nil
	}

	return "No symbols found", nil
}

func discoverSourceFiles(workingDir string, extensions []string, maxFiles int) []string {
	extSet := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		extSet["."+ext] = true
	}

	var files []string
	filepath.Walk(workingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__" || name == "target" || name == "build" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}

		if extSet[filepath.Ext(path)] {
			files = append(files, path)
			if len(files) >= maxFiles {
				return filepath.SkipAll
			}
		}

		return nil
	})

	return files
}
