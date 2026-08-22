package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	starting   map[string]*sessionStart
	restarts   map[string]int
	warming    map[string]bool
	closed     bool
	mu         sync.Mutex

	detectMu              sync.Mutex
	roots                 []projectRoot
	detectedAt            time.Time
	initializationOptions map[string][]byte
	commandResolver       func(string) string
}

type ManagerOption func(*Manager)

// WithServerInitializationOptions adds opaque initialization options to every
// detected server with the given name. The options are marshalled once and
// copied into each server descriptor before a session starts.
func WithServerInitializationOptions(name string, value any) ManagerOption {
	return func(manager *Manager) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return
		}
		if manager.initializationOptions == nil {
			manager.initializationOptions = make(map[string][]byte)
		}
		manager.initializationOptions[name] = encoded
	}
}

// WithCommandResolver adds an application-managed fallback after project and
// standard system command discovery.
func WithCommandResolver(resolve func(string) string) ManagerOption {
	return func(manager *Manager) {
		manager.commandResolver = resolve
	}
}

type sessionStart struct {
	done    chan struct{}
	session *Session
	err     error
}

const detectionCacheTTL = 30 * time.Second

func NewManager(workingDir string, options ...ManagerOption) *Manager {
	manager := &Manager{
		workingDir: workingDir,
		sessions:   make(map[string]*Session),
		starting:   make(map[string]*sessionStart),
		restarts:   make(map[string]int),
		warming:    make(map[string]bool),
	}
	for _, option := range options {
		if option != nil {
			option(manager)
		}
	}
	return manager
}

func (m *Manager) WorkingDir() string {
	return m.workingDir
}

func (m *Manager) detect() []projectRoot {
	m.detectMu.Lock()
	defer m.detectMu.Unlock()
	if m.detectedAt.IsZero() || time.Since(m.detectedAt) >= detectionCacheTTL {
		m.roots = detectAll(m.workingDir, m.commandResolver)
		for index := range m.roots {
			if options := m.initializationOptions[m.roots[index].Server.Name]; len(options) > 0 {
				m.roots[index].Server.InitializationOptions = slices.Clone(options)
			}
		}
		m.detectedAt = time.Now()
	}
	return slices.Clone(m.roots)
}

// InvalidateDetection makes newly installed or updated servers visible on the
// next lookup without waiting for the detection cache TTL. Crash counters are
// reset because an updated tool deserves a fresh restart budget.
func (m *Manager) InvalidateDetection() {
	m.detectMu.Lock()
	m.detectedAt = time.Time{}
	m.detectMu.Unlock()
	m.mu.Lock()
	clear(m.restarts)
	m.mu.Unlock()
}

// SetServerInitializationOptions updates future sessions for a server. An
// active session for that server is closed because LSP initialization options
// cannot be changed after startup.
func (m *Manager) SetServerInitializationOptions(name string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.detectMu.Lock()
	if bytes.Equal(m.initializationOptions[name], encoded) {
		m.detectMu.Unlock()
		return nil
	}
	if m.initializationOptions == nil {
		m.initializationOptions = make(map[string][]byte)
	}
	m.initializationOptions[name] = encoded
	m.detectedAt = time.Time{}
	m.detectMu.Unlock()

	var stale []*Session
	m.mu.Lock()
	for key, session := range m.sessions {
		if session != nil && strings.EqualFold(session.server.Name, name) {
			stale = append(stale, session)
			delete(m.sessions, key)
		}
	}
	m.mu.Unlock()
	for _, session := range stale {
		session.Close()
	}
	return nil
}

func (m *Manager) FindServer(filePath string) *Server {
	project := m.findProject(filePath)
	if project == nil {
		return nil
	}
	server := project.Server
	return &server
}

func (m *Manager) findProject(filePath string) *projectRoot {
	return findProject(m.detect(), filePath)
}

func findProject(projects []projectRoot, filePath string) *projectRoot {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))
	if ext == "" {
		return nil
	}

	dir := filepath.Dir(filePath)
	var best *projectRoot
	bestLen := -1

	for _, root := range projects {
		if !isSubPath(root.Dir, dir) {
			continue
		}
		if len(root.Dir) <= bestLen {
			continue
		}
		if !slices.Contains(root.Server.Languages, ext) {
			continue
		}
		candidate := root
		best = &candidate
		bestLen = len(root.Dir)
	}

	return best
}

func (m *Manager) DetectServers() []Server {
	var servers []Server
	seen := make(map[string]bool)

	for _, root := range m.detect() {
		key := serverKey(root.Server)
		if seen[key] {
			continue
		}
		seen[key] = true
		servers = append(servers, root.Server)
	}

	return servers
}

func (m *Manager) Projects() []Project { return m.detect() }

func (m *Manager) ProjectSession(ctx context.Context, project Project) (*Session, error) {
	return m.getSession(ctx, project)
}

func (m *Manager) WarmUpFile(filePath string) {
	if project := m.findProject(filePath); project != nil {
		m.warmUp(*project)
	}
}

func projectKey(project projectRoot) string {
	return filepath.Clean(project.Dir) + "\x00" + serverKey(project.Server)
}

func serverKey(server Server) string {
	return server.Command + "\x00" + strings.Join(server.Args, "\x00") + "\x00" + string(server.InitializationOptions)
}

func (m *Manager) GetSession(ctx context.Context, filePath string) (*Session, error) {
	project := m.findProject(filePath)
	if project == nil {
		return nil, fmt.Errorf("no LSP server found for file: %s", filePath)
	}

	return m.getSession(ctx, *project)
}

func (m *Manager) getSession(ctx context.Context, project projectRoot) (*Session, error) {
	server := project.Server
	key := projectKey(project)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("LSP manager is closed")
	}
	if session := m.sessions[key]; session != nil && session.IsAlive() {
		m.mu.Unlock()
		return session, nil
	}
	if start := m.starting[key]; start != nil {
		m.mu.Unlock()
		select {
		case <-start.done:
			return start.session, start.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	var openedDocuments []openDocument
	restarting := false
	if previous := m.sessions[key]; previous != nil {
		openedDocuments = previous.openedDocuments()
		delete(m.sessions, key)
		m.restarts[key]++
		restarting = true
	}
	if m.restarts[key] > maxRestarts {
		m.mu.Unlock()
		return nil, fmt.Errorf("LSP server %s crashed %d times, not restarting", server.Name, maxRestarts)
	}
	start := &sessionStart{done: make(chan struct{})}
	m.starting[key] = start
	m.mu.Unlock()

	session, err := connect(ctx, project.Dir, server)
	if err != nil && restarting {
		err = fmt.Errorf("restart %s: %w", server.Name, err)
	}
	if err == nil {
		for _, document := range openedDocuments {
			if document.Path == "" {
				continue
			}
			if document.Saved {
				_, _ = session.SaveDocument(ctx, document.Path, document.Content)
			} else {
				_, _ = session.SyncDocument(ctx, document.Path, document.Content)
			}
		}
	}

	m.mu.Lock()
	if m.closed {
		err = fmt.Errorf("LSP manager is closed")
	}
	if err == nil {
		m.sessions[key] = session
	}
	start.session = session
	start.err = err
	if err != nil {
		start.session = nil
	}
	delete(m.starting, key)
	close(start.done)
	m.mu.Unlock()

	if err != nil {
		if session != nil {
			session.Close()
		}
		return nil, err
	}
	return session, nil
}

func (m *Manager) ActiveSession(filePath string) (*Session, bool) {
	project := m.findProject(filePath)
	if project == nil {
		return nil, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[projectKey(*project)]; ok && session.IsAlive() {
		return session, true
	}
	return nil, false
}

func (m *Manager) WarmUpServers() {
	go func() {
		for _, project := range m.detect() {
			m.warmUp(project)
		}
	}()
}

func (m *Manager) warmUp(project projectRoot) {
	key := projectKey(project)
	m.mu.Lock()
	if m.closed || m.warming[key] {
		m.mu.Unlock()
		return
	}
	m.warming[key] = true
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.warming, key)
			m.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*startupTimeout)
		defer cancel()
		m.getSession(ctx, project)
	}()
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	clear(m.sessions)
	m.mu.Unlock()

	for _, session := range sessions {
		session.Close()
	}
}
