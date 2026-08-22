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
	lifecycle  context.Context
	cancel     context.CancelFunc
	connect    sessionConnector
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
		manager.initializationOptions[serverOptionsKey(name)] = encoded
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

type sessionConnector func(context.Context, string, Server) (*Session, error)

const detectionCacheTTL = 30 * time.Second

func NewManager(workingDir string, options ...ManagerOption) *Manager {
	lifecycle, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		workingDir: workingDir,
		lifecycle:  lifecycle,
		cancel:     cancel,
		connect:    connect,
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
			if options := m.initializationOptions[serverOptionsKey(m.roots[index].Server.Name)]; len(options) > 0 {
				m.roots[index].Server.InitializationOptions = slices.Clone(options)
			}
		}
		m.detectedAt = time.Now()
	}
	return cloneProjectRoots(m.roots)
}

// Detection results are cached, but every public lookup returns an isolated
// descriptor. Server contains slices, so a shallow copy would let a caller
// mutate the cache (and, for catalog-backed fields, the global catalog) by
// changing Args, Languages, or InitializationOptions in a returned value.
func cloneProjectRoots(values []projectRoot) []projectRoot {
	cloned := make([]projectRoot, len(values))
	for index, value := range values {
		cloned[index] = value
		cloned[index].Server.Args = slices.Clone(value.Server.Args)
		cloned[index].Server.Languages = slices.Clone(value.Server.Languages)
		cloned[index].Server.InitializationOptions = slices.Clone(value.Server.InitializationOptions)
	}
	return cloned
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
	optionsKey := serverOptionsKey(name)
	m.detectMu.Lock()
	if bytes.Equal(m.initializationOptions[optionsKey], encoded) {
		m.detectMu.Unlock()
		return nil
	}
	if m.initializationOptions == nil {
		m.initializationOptions = make(map[string][]byte)
	}
	m.initializationOptions[optionsKey] = encoded
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
	closeSessions(stale)
	return nil
}

func serverOptionsKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// serverInitializationOptionsCurrentLocked reports whether a descriptor still
// carries the manager's current options. Callers hold detectMu so an options
// update cannot race the decision to publish a newly connected session.
func (m *Manager) serverInitializationOptionsCurrentLocked(server Server) bool {
	options, configured := m.initializationOptions[serverOptionsKey(server.Name)]
	return !configured || bytes.Equal(server.InitializationOptions, options)
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

	connectCtx, cancelConnect := context.WithCancel(ctx)
	stopCloseCancellation := func() bool { return false }
	if m.lifecycle != nil {
		stopCloseCancellation = context.AfterFunc(m.lifecycle, cancelConnect)
	}
	connector := m.connect
	if connector == nil {
		connector = connect
	}
	session, err := connector(connectCtx, project.Dir, server)
	if err == nil && session == nil {
		err = fmt.Errorf("start %s: connector returned no session", server.Name)
	}
	if err == nil {
		err = connectCtx.Err()
	}
	if err == nil {
		for _, document := range openedDocuments {
			if document.Path == "" {
				continue
			}
			var syncErr error
			if document.Saved {
				_, syncErr = session.SaveDocument(connectCtx, document.Path, document.Content)
			} else {
				_, syncErr = session.SyncDocument(connectCtx, document.Path, document.Content)
			}
			if syncErr != nil {
				err = fmt.Errorf("restore open document %s: %w", document.Path, syncErr)
				break
			}
		}
	}
	_ = stopCloseCancellation()
	cancelConnect()
	if err != nil && restarting {
		err = fmt.Errorf("restart %s: %w", server.Name, err)
	}

	m.detectMu.Lock()
	m.mu.Lock()
	if m.closed {
		err = fmt.Errorf("LSP manager is closed")
	} else if err == nil && !m.serverInitializationOptionsCurrentLocked(server) {
		err = fmt.Errorf("LSP server %s initialization options changed during startup", server.Name)
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
	m.detectMu.Unlock()

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
		if m.lifecycle != nil {
			select {
			case <-m.lifecycle.Done():
				return
			default:
			}
		}
		for _, project := range m.detect() {
			if m.lifecycle != nil {
				select {
				case <-m.lifecycle.Done():
					return
				default:
				}
			}
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

		parent := m.lifecycle
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, 2*startupTimeout)
		defer cancel()
		m.getSession(ctx, project)
	}()
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	clear(m.sessions)
	m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}

	closeSessions(sessions)
}

func closeSessions(sessions []*Session) {
	var wait sync.WaitGroup
	for _, session := range sessions {
		if session == nil {
			continue
		}
		wait.Go(session.Close)
	}
	wait.Wait()
}
