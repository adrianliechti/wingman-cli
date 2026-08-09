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

const (
	workspaceDiagnosticsMaxFiles = 50
	sourceDiscoveryCountLimit    = 2000
)

type Manager struct {
	workingDir string
	sessions   map[string]*Session
	starting   map[string]*sessionStart
	restarts   map[string]int
	warming    map[string]bool
	closed     bool
	mu         sync.Mutex

	detectMu   sync.Mutex
	roots      []projectRoot
	detectedAt time.Time
}

type sessionStart struct {
	done    chan struct{}
	session *Session
	err     error
}

const detectionCacheTTL = 30 * time.Second

func NewManager(workingDir string) *Manager {
	return &Manager{
		workingDir: workingDir,
		sessions:   make(map[string]*Session),
		starting:   make(map[string]*sessionStart),
		restarts:   make(map[string]int),
		warming:    make(map[string]bool),
	}
}

func (m *Manager) WorkingDir() string {
	return m.workingDir
}

func (m *Manager) detect() []projectRoot {
	m.detectMu.Lock()
	defer m.detectMu.Unlock()
	if m.detectedAt.IsZero() || time.Since(m.detectedAt) >= detectionCacheTTL {
		m.roots = detectAll(m.workingDir)
		m.detectedAt = time.Now()
	}
	return slices.Clone(m.roots)
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

func (m *Manager) projects() []projectRoot {
	projects := m.detect()
	result := make([]projectRoot, 0, len(projects))
	seen := make(map[string]bool)
	for _, project := range projects {
		key := projectKey(project)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, project)
	}
	return result
}

func projectKey(project projectRoot) string {
	return filepath.Clean(project.Dir) + "\x00" + serverKey(project.Server)
}

func serverKey(server Server) string {
	return server.Command + "\x00" + strings.Join(server.Args, "\x00")
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

	var openedURIs []string
	restartCount := m.restarts[key]
	restarting := false
	if previous := m.sessions[key]; previous != nil {
		openedURIs = previous.OpenedDocURIs()
		delete(m.sessions, key)
		restarting = true
		if restartCount >= maxRestarts {
			m.mu.Unlock()
			return nil, fmt.Errorf("LSP server %s crashed %d times, not restarting", server.Name, restartCount)
		}
	}
	start := &sessionStart{done: make(chan struct{})}
	m.starting[key] = start
	m.mu.Unlock()

	session, err := connect(ctx, project.Dir, server)
	if err != nil && restarting {
		err = fmt.Errorf("restart %s: %w", server.Name, err)
	}
	if err == nil {
		for _, uri := range openedURIs {
			if path := uriToPath(uri); path != "" {
				_, _ = session.OpenDocument(ctx, path)
			}
		}
	}

	m.mu.Lock()
	if m.closed {
		err = fmt.Errorf("LSP manager is closed")
	}
	if err == nil {
		m.sessions[key] = session
		if restarting {
			m.restarts[key] = restartCount + 1
		}
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
		for _, project := range m.projects() {
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

// PostEditDiagnostics reports current errors in an edited file, for attaching
// to edit/write tool results. It never cold-starts a server synchronously: if
// none is running yet it warms one up in the background and returns "".
func (m *Manager) PostEditDiagnostics(ctx context.Context, path string) string {
	project := m.findProject(path)
	if project == nil {
		return ""
	}

	session, ok := m.ActiveSession(path)
	if !ok {
		m.warmUp(*project)
		return ""
	}

	uri, err := session.OpenDocument(ctx, path)
	if err != nil {
		return ""
	}

	diags, known := session.WaitForDiagnostics(ctx, uri, 2*time.Second)
	if !known {
		return ""
	}

	var errs []Diagnostic
	for _, diag := range diags {
		if diag.Severity == DiagnosticSeverityError || diag.Severity == 0 {
			errs = append(errs, diag)
		}
	}
	if len(errs) == 0 {
		return ""
	}

	displayPath := relPath(m.workingDir, path)

	var sb strings.Builder
	fmt.Fprintf(&sb, "lsp diagnostics for %s (%s):\n", displayPath, severitySummary(errs))

	shown := errs
	if len(shown) > 10 {
		shown = shown[:10]
	}
	for _, diag := range shown {
		fmt.Fprintf(&sb, "  %s\n", formatDiagnosticLine(displayPath, diag))
	}
	if len(errs) > len(shown) {
		fmt.Fprintf(&sb, "  ... and %d more\n", len(errs)-len(shown))
	}

	return strings.TrimRight(sb.String(), "\n")
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

func (m *Manager) openDiscoveredFiles(ctx context.Context, project projectRoot, projects []projectRoot, session *Session) ([]string, []string, int, bool) {
	projectID := projectKey(project)
	files, total, truncated := discoverSourceFilesMatching(ctx, project.Dir, project.Server.Languages, workspaceDiagnosticsMaxFiles, func(path string) bool {
		owner := findProject(projects, path)
		return owner != nil && projectKey(*owner) == projectID
	})

	opened := make([]string, 0, len(files))
	uris := make([]string, 0, len(files))
	for _, file := range files {
		if ctx.Err() != nil {
			break
		}
		uri, err := session.OpenDocument(ctx, file)
		if err != nil {
			continue
		}
		opened = append(opened, file)
		uris = append(uris, uri)
	}

	waitForPushedDiagnostics(ctx, session, uris, 5*time.Second)

	return opened, uris, total, truncated
}

func waitForPushedDiagnostics(ctx context.Context, session *Session, uris []string, timeout time.Duration) {
	if session.SupportsPullDiagnostics() {
		return
	}
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

type WorkspaceDiagnosticsReport struct {
	Diagnostics        map[string][]Diagnostic
	CheckedFiles       int
	DiscoveredFiles    int
	DiscoveryTruncated bool
	UnknownFiles       int
	UnavailableServers []string
}

func (m *Manager) CollectAllDiagnostics(ctx context.Context) WorkspaceDiagnosticsReport {
	report := WorkspaceDiagnosticsReport{Diagnostics: make(map[string][]Diagnostic)}
	projects := m.projects()
	unavailable := make(map[string]bool)

	for _, project := range projects {
		if ctx.Err() != nil {
			break
		}
		session, err := m.getSession(ctx, project)
		if err != nil {
			unavailable[m.projectLabel(project)] = true
			continue
		}

		files, uris, total, truncated := m.openDiscoveredFiles(ctx, project, projects, session)
		report.CheckedFiles += len(files)
		report.DiscoveredFiles += total
		report.DiscoveryTruncated = report.DiscoveryTruncated || truncated
		states := collectDiagnosticStates(ctx, session, uris)
		for i, file := range files {
			diags, known := states[i].diagnostics, states[i].known
			if !known {
				report.UnknownFiles++
				continue
			}
			if len(diags) > 0 {
				report.Diagnostics[file] = diags
			}
		}
	}
	for name := range unavailable {
		report.UnavailableServers = append(report.UnavailableServers, name)
	}
	slices.Sort(report.UnavailableServers)

	return report
}

func (m *Manager) projectLabel(project projectRoot) string {
	rel := relPath(m.workingDir, project.Dir)
	if rel == "." {
		return project.Server.Name
	}
	return fmt.Sprintf("%s (%s)", project.Server.Name, rel)
}

type diagnosticState struct {
	diagnostics []Diagnostic
	known       bool
}

func collectDiagnosticStates(ctx context.Context, session *Session, uris []string) []diagnosticState {
	states := make([]diagnosticState, len(uris))
	if len(uris) == 0 {
		return states
	}

	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(uris)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				states[i].diagnostics, states[i].known = session.DiagnosticsState(ctx, uris[i])
			}
		}()
	}
	for i := range uris {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return states
		}
	}
	close(jobs)
	workers.Wait()
	return states
}

func (m *Manager) WorkspaceDiagnostics(ctx context.Context) (string, error) {
	if len(m.projects()) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	type fileDiagnostic struct {
		path string
		diag Diagnostic
	}

	report := m.CollectAllDiagnostics(ctx)
	var all []fileDiagnostic
	for file, diagnostics := range report.Diagnostics {
		displayPath := relPath(m.workingDir, file)
		for _, diag := range diagnostics {
			all = append(all, fileDiagnostic{path: displayPath, diag: diag})
		}
	}

	var notes []string
	if report.DiscoveredFiles > report.CheckedFiles || report.DiscoveryTruncated {
		total := fmt.Sprintf("%d", report.DiscoveredFiles)
		if report.DiscoveryTruncated {
			total += "+"
		}
		notes = append(notes, fmt.Sprintf("checked %d of %s source files; results are partial", report.CheckedFiles, total))
	} else {
		notes = append(notes, fmt.Sprintf("checked %d source files", report.CheckedFiles))
	}
	if report.UnknownFiles > 0 {
		notes = append(notes, fmt.Sprintf("%d files returned no data", report.UnknownFiles))
	}
	if len(report.UnavailableServers) > 0 {
		notes = append(notes, "server unavailable: "+strings.Join(report.UnavailableServers, ", "))
	}
	coverage := "Coverage: " + strings.Join(notes, "; ")

	if len(all) == 0 {
		return "No workspace diagnostics found\n" + coverage, nil
	}

	slices.SortStableFunc(all, func(a, b fileDiagnostic) int {
		return severityRank(a.diag.Severity) - severityRank(b.diag.Severity)
	})

	diags := make([]Diagnostic, len(all))
	for i, fd := range all {
		diags[i] = fd.diag
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Workspace Diagnostics (%d found: %s):\n", len(all), severitySummary(diags))
	for _, fd := range all {
		fmt.Fprintf(&sb, "  %s\n", formatDiagnosticLine(fd.path, fd.diag))
	}
	sb.WriteString(coverage)

	return sb.String(), nil
}

func (m *Manager) WorkspaceSymbols(ctx context.Context, query string) (string, error) {
	projects := m.projects()
	if len(projects) == 0 {
		return "", fmt.Errorf("no LSP servers detected in workspace")
	}

	var allSymInfos []SymbolInformation
	var allWsSymbols []WorkspaceSymbol

	for _, project := range projects {
		session, err := m.getSession(ctx, project)
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

func discoverSourceFiles(workingDir string, extensions []string, maxFiles int) ([]string, int, bool) {
	return discoverSourceFilesMatching(context.Background(), workingDir, extensions, maxFiles, nil)
}

func discoverSourceFilesMatching(ctx context.Context, workingDir string, extensions []string, maxFiles int, include func(string) bool) ([]string, int, bool) {
	extSet := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		extSet["."+strings.ToLower(ext)] = true
	}

	var files []string
	total := 0
	truncated := false
	filepath.WalkDir(workingDir, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
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

		if extSet[strings.ToLower(filepath.Ext(path))] && (include == nil || include(path)) {
			total++
			if total > sourceDiscoveryCountLimit {
				total = sourceDiscoveryCountLimit
				truncated = true
				return filepath.SkipAll
			}
			if len(files) < maxFiles {
				files = append(files, path)
			}
		}

		return nil
	})

	return files, total, truncated
}
