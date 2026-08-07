package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const maxRestarts = 3

type Manager struct {
	workingDir string
	sessions   map[string]*Session
	restarts   map[string]int
	mu         sync.Mutex

	roots      []projectRoot
	detectOnce sync.Once
}

func NewManager(workingDir string) *Manager {
	return &Manager{
		workingDir: workingDir,
		sessions:   make(map[string]*Session),
		restarts:   make(map[string]int),
	}
}

func (m *Manager) WorkingDir() string {
	return m.workingDir
}

func (m *Manager) detect() []projectRoot {
	m.detectOnce.Do(func() {
		m.roots = detectAll(m.workingDir)
	})
	return m.roots
}

func (m *Manager) FindServer(filePath string) *Server {
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	if ext == "" {
		return nil
	}

	dir := filepath.Dir(filePath)
	var best *Server
	bestLen := -1

	for _, root := range m.detect() {
		if !isSubPath(root.Dir, dir) {
			continue
		}
		if len(root.Dir) <= bestLen {
			continue
		}
		if !slices.Contains(root.Server.Languages, ext) {
			continue
		}
		srv := root.Server
		best = &srv
		bestLen = len(root.Dir)
	}

	return best
}

func (m *Manager) DetectServers() []Server {
	var servers []Server
	seen := make(map[string]bool)

	for _, root := range m.detect() {
		if seen[root.Server.Command] {
			continue
		}
		seen[root.Server.Command] = true
		servers = append(servers, root.Server)
	}

	return servers
}

func (m *Manager) GetSession(ctx context.Context, filePath string) (*Session, error) {
	server := m.FindServer(filePath)
	if server == nil {
		return nil, fmt.Errorf("no LSP server found for file: %s", filePath)
	}

	return m.GetSessionByServer(ctx, *server)
}

func (m *Manager) GetSessionByServer(ctx context.Context, server Server) (*Session, error) {
	key := server.Command

	m.mu.Lock()
	if session, ok := m.sessions[key]; ok {
		if session.IsAlive() {
			m.mu.Unlock()
			return session, nil
		}

		openedURIs := session.OpenedDocURIs()
		restartCount := m.restarts[key]
		delete(m.sessions, key)
		m.mu.Unlock()

		if restartCount >= maxRestarts {
			return nil, fmt.Errorf("LSP server %s crashed %d times, not restarting", server.Name, restartCount)
		}

		newSession, err := connect(ctx, m.workingDir, server)
		if err != nil {
			return nil, fmt.Errorf("restart %s: %w", server.Name, err)
		}

		m.mu.Lock()

		if existing, ok := m.sessions[key]; ok && existing.IsAlive() {
			m.mu.Unlock()
			newSession.Close()
			return existing, nil
		}
		m.sessions[key] = newSession
		m.restarts[key] = restartCount + 1
		m.mu.Unlock()

		for _, uri := range openedURIs {
			path := uriToPath(uri)
			if path != "" {
				newSession.OpenDocument(ctx, path)
			}
		}

		return newSession, nil
	}
	m.mu.Unlock()

	session, err := connect(ctx, m.workingDir, server)
	if err != nil {
		return nil, err
	}

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

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, session := range m.sessions {
		session.Close()
		delete(m.sessions, key)
	}
}

func (m *Manager) openDiscoveredFiles(ctx context.Context, session *Session, languages []string) ([]string, []string, int) {
	files, total := discoverSourceFiles(m.workingDir, languages, 50)

	opened := make([]string, 0, len(files))
	uris := make([]string, 0, len(files))
	for _, file := range files {
		uri, err := session.OpenDocument(ctx, file)
		if err != nil {
			continue
		}
		opened = append(opened, file)
		uris = append(uris, uri)
	}

	waitForPushedDiagnostics(ctx, session, uris, 5*time.Second)

	return opened, uris, total
}

func waitForPushedDiagnostics(ctx context.Context, session *Session, uris []string, timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		pending := 0
		for _, uri := range uris {
			if !session.PushSeen(uri) {
				pending++
			}
		}
		if pending == 0 {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) CollectAllDiagnostics(ctx context.Context) map[string][]Diagnostic {
	servers := m.DetectServers()
	result := make(map[string][]Diagnostic)

	for _, server := range servers {
		session, err := m.GetSessionByServer(ctx, server)
		if err != nil {
			continue
		}

		files, uris, _ := m.openDiscoveredFiles(ctx, session, server.Languages)
		for i, file := range files {
			diags := session.CollectDiagnostics(ctx, uris[i])
			if len(diags) > 0 {
				result[file] = diags
			}
		}
	}

	return result
}

func (m *Manager) WorkspaceDiagnostics(ctx context.Context) (string, error) {
	servers := m.DetectServers()
	if len(servers) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	var sb strings.Builder
	totalDiags := 0
	checkedFiles := 0
	totalFiles := 0
	unknownFiles := 0
	var unavailable []string

	for _, server := range servers {
		session, err := m.GetSessionByServer(ctx, server)
		if err != nil {
			unavailable = append(unavailable, server.Name)
			continue
		}

		files, uris, total := m.openDiscoveredFiles(ctx, session, server.Languages)
		checkedFiles += len(files)
		totalFiles += total

		for i, file := range files {
			diags, known := session.DiagnosticsState(ctx, uris[i])
			if !known {
				unknownFiles++
				continue
			}

			displayPath := relPath(m.workingDir, file)
			for _, diag := range diags {
				totalDiags++
				fmt.Fprintf(&sb, "  %s\n", formatDiagnosticLine(displayPath, diag))
			}
		}
	}

	var notes []string
	if totalFiles > checkedFiles {
		notes = append(notes, fmt.Sprintf("checked %d of %d source files; results are partial", checkedFiles, totalFiles))
	} else {
		notes = append(notes, fmt.Sprintf("checked %d source files", checkedFiles))
	}
	if unknownFiles > 0 {
		notes = append(notes, fmt.Sprintf("%d files returned no data", unknownFiles))
	}
	if len(unavailable) > 0 {
		notes = append(notes, "server unavailable: "+strings.Join(unavailable, ", "))
	}
	coverage := "Coverage: " + strings.Join(notes, "; ")

	if totalDiags == 0 {
		return "No workspace diagnostics found\n" + coverage, nil
	}

	return fmt.Sprintf("Workspace Diagnostics (%d found):\n%s%s", totalDiags, sb.String(), coverage), nil
}

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

		var symInfos []SymbolInformation
		if err := unmarshalResult(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
			allSymInfos = append(allSymInfos, symInfos...)
			continue
		}

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

var skippedDiscoveryDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	"target":       true,
	"build":        true,
	"dist":         true,
}

func discoverSourceFiles(workingDir string, extensions []string, maxFiles int) ([]string, int) {
	const countLimit = 2000

	extSet := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		extSet["."+ext] = true
	}

	var files []string
	total := 0
	filepath.WalkDir(workingDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			if path != workingDir && (strings.HasPrefix(name, ".") || skippedDiscoveryDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}

		if extSet[filepath.Ext(path)] {
			total++
			if len(files) < maxFiles {
				files = append(files, path)
			}
			if total >= countLimit {
				return filepath.SkipAll
			}
		}

		return nil
	})

	return files, total
}
