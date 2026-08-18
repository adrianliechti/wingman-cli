package dap

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const detectionCacheTTL = 30 * time.Second

type detectedAdapter struct {
	adapter  Adapter
	projects []string
}

type Manager struct {
	root     string
	adapters []Adapter
	lookup   func(string) string
	start    sessionStarter

	detectMu   sync.Mutex
	detected   []detectedAdapter
	detectedAt time.Time

	mu          sync.Mutex
	sessions    map[string]*Session
	active      string
	breakpoints map[string][]SourceBreakpoint
	closed      bool
}

type sessionStarter func(context.Context, string, Plan, StartOptions) (*Session, error)

func NewManager(root string) *Manager {
	return newManager(root, knownAdapters, resolveAdapterCommand, startSession)
}

func newManager(root string, adapters []Adapter, lookup func(string) string, starter sessionStarter) *Manager {
	return &Manager{
		root:        filepath.Clean(root),
		adapters:    cloneAdapters(adapters),
		lookup:      lookup,
		start:       starter,
		sessions:    make(map[string]*Session),
		breakpoints: make(map[string][]SourceBreakpoint),
	}
}

func (m *Manager) WorkingDir() string { return m.root }

func (m *Manager) detect(ctx context.Context) ([]detectedAdapter, error) {
	m.detectMu.Lock()
	defer m.detectMu.Unlock()
	if !m.detectedAt.IsZero() && time.Since(m.detectedAt) < detectionCacheTTL {
		return cloneDetected(m.detected), nil
	}

	var detected []detectedAdapter
	for _, candidate := range m.adapters {
		projects, err := detectProjects(ctx, m.root, candidate.Markers)
		if err != nil {
			return nil, fmt.Errorf("detect %s projects: %w", candidate.Language, err)
		}
		if len(projects) == 0 {
			continue
		}
		command := m.lookup(candidate.Command)
		if command == "" {
			continue
		}
		candidate.Command = command
		detected = append(detected, detectedAdapter{adapter: candidate, projects: projects})
	}
	m.detected = detected
	m.detectedAt = time.Now()
	return cloneDetected(detected), nil
}

func cloneAdapters(values []Adapter) []Adapter {
	cloned := make([]Adapter, len(values))
	for i, value := range values {
		cloned[i] = value
		cloned[i].Args = slices.Clone(value.Args)
		cloned[i].Markers = slices.Clone(value.Markers)
		cloned[i].Defaults = maps.Clone(value.Defaults)
		cloned[i].ConfigurationPaths = slices.Clone(value.ConfigurationPaths)
	}
	return cloned
}

func cloneDetected(values []detectedAdapter) []detectedAdapter {
	cloned := make([]detectedAdapter, len(values))
	for i, value := range values {
		cloned[i] = value
		cloned[i].adapter = cloneAdapters([]Adapter{value.adapter})[0]
		cloned[i].projects = slices.Clone(value.projects)
	}
	return cloned
}

func (m *Manager) Adapters(ctx context.Context) ([]AdapterInfo, error) {
	values, err := m.detect(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]AdapterInfo, 0, len(values))
	for _, value := range values {
		result = append(result, AdapterInfo{
			Name:               value.adapter.Name,
			Language:           value.adapter.Language,
			Command:            value.adapter.Command,
			Projects:           slices.Clone(value.projects),
			ConfigurationPaths: slices.Clone(value.adapter.ConfigurationPaths),
			ConfigurationHint:  value.adapter.ConfigurationHint,
		})
	}
	return result, nil
}

func (m *Manager) Start(ctx context.Context, options StartOptions) (*Session, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("DAP manager is closed")
	}
	for path, breakpoints := range m.breakpoints {
		if _, supplied := options.Breakpoints[path]; !supplied {
			if options.Breakpoints == nil {
				options.Breakpoints = make(map[string][]SourceBreakpoint)
			}
			options.Breakpoints[path] = slices.Clone(breakpoints)
		}
	}
	m.mu.Unlock()

	values, err := m.detect(ctx)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, errors.New("no debug adapter detected in this workspace")
	}
	selected, err := selectAdapter(values, options.Adapter)
	if err != nil {
		return nil, err
	}
	plan, err := resolvePlan(m.root, selected, options)
	if err != nil {
		return nil, err
	}

	id := uuid.NewString()[:8]
	session, err := m.start(ctx, id, plan, options)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		session.Close()
		return nil, errors.New("DAP manager is closed")
	}
	m.sessions[id] = session
	m.active = id
	m.mu.Unlock()
	return session, nil
}

func selectAdapter(values []detectedAdapter, requested string) (detectedAdapter, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" && requested != "auto" {
		for _, value := range values {
			if strings.EqualFold(value.adapter.Name, requested) || strings.EqualFold(value.adapter.Language, requested) {
				return value, nil
			}
		}
		return detectedAdapter{}, fmt.Errorf("debug adapter %q is not available", requested)
	}
	if len(values) == 1 {
		return values[0], nil
	}
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.adapter.Name)
	}
	slices.Sort(names)
	return detectedAdapter{}, fmt.Errorf("multiple debug adapters are available; choose one of: %s", strings.Join(names, ", "))
}

func resolvePlan(workspace string, selected detectedAdapter, options StartOptions) (Plan, error) {
	requestName := strings.ToLower(strings.TrimSpace(options.Request))
	if requestName == "" {
		requestName = "launch"
	}
	if requestName != "launch" && requestName != "attach" {
		return Plan{}, fmt.Errorf("request must be launch or attach (got %q)", options.Request)
	}

	projectDir, err := selectProjectDir(workspace, selected.projects, options.ProjectDir, options.Configuration)
	if err != nil {
		return Plan{}, err
	}
	arguments := maps.Clone(selected.adapter.Defaults)
	if arguments == nil {
		arguments = make(map[string]any)
	}
	for key, value := range options.Configuration {
		arguments[key] = value
	}
	arguments, err = ResolveConfigurationPaths(workspace, projectDir, selected.adapter.ConfigurationPaths, arguments)
	if err != nil {
		return Plan{}, err
	}
	if _, exists := arguments["name"]; !exists {
		arguments["name"] = "Wingman AI debug"
	}
	arguments["request"] = requestName

	target, _ := arguments["program"].(string)
	mode, _ := arguments["mode"].(string)
	return Plan{
		Adapter:    selected.adapter,
		ProjectDir: projectDir,
		Target:     target,
		Mode:       mode,
		Request:    requestName,
		Arguments:  arguments,
	}, nil
}

// ResolveConfigurationPaths validates and resolves the path fields declared by
// an adapter descriptor. All other adapter configuration remains opaque.
func ResolveConfigurationPaths(workspace, projectDir string, fields []ConfigurationPath, configuration map[string]any) (map[string]any, error) {
	resolved := maps.Clone(configuration)
	if resolved == nil {
		resolved = make(map[string]any)
	}
	for _, field := range fields {
		value, ok := resolved[field.Key].(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			continue
		}
		if strings.Contains(value, "${") {
			return nil, fmt.Errorf("configuration %s must be a concrete workspace path", field.Key)
		}
		path := filepath.FromSlash(value)
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectDir, path)
		}
		path, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve configuration %s: %w", field.Key, err)
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("configuration %s must stay inside the workspace", field.Key)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("configuration %s path %q does not exist", field.Key, value)
		}
		if field.Directory && !info.IsDir() {
			return nil, fmt.Errorf("configuration %s path %q must be a directory", field.Key, value)
		}
		resolved[field.Key] = filepath.Clean(path)
	}
	return resolved, nil
}

func selectProjectDir(workspace string, projects []string, requested string, configuration map[string]any) (string, error) {
	if strings.TrimSpace(requested) != "" {
		path, err := workspaceDirectory(workspace, requested)
		if err != nil {
			return "", err
		}
		return path, nil
	}

	for _, key := range []string{"cwd", "dlvCwd", "program"} {
		value, _ := configuration[key].(string)
		if value == "" {
			continue
		}
		path := value
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		if info, err := os.Stat(path); err == nil {
			if !info.IsDir() {
				path = filepath.Dir(path)
			}
			if project := nearestProject(projects, path); project != "" {
				return project, nil
			}
		}
	}
	if len(projects) == 1 {
		return projects[0], nil
	}
	if len(projects) == 0 {
		return "", errors.New("adapter has no detected project root")
	}
	relative := make([]string, 0, len(projects))
	for _, project := range projects {
		rel, err := filepath.Rel(workspace, project)
		if err != nil {
			rel = project
		}
		relative = append(relative, filepath.ToSlash(rel))
	}
	return "", fmt.Errorf("multiple adapter project roots found; set project_dir to one of: %s", strings.Join(relative, ", "))
}

func workspaceDirectory(workspace, value string) (string, error) {
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve project_dir: %w", err)
	}
	rel, err := filepath.Rel(workspace, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("project_dir must stay inside the workspace")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("project_dir %q: %w", value, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project_dir %q is not a directory", value)
	}
	return filepath.Clean(abs), nil
}

func nearestProject(projects []string, target string) string {
	best := ""
	for _, project := range projects {
		rel, err := filepath.Rel(project, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(project) > len(best) {
			best = project
		}
	}
	return best
}

func (m *Manager) Session(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("DAP manager is closed")
	}
	if id == "" {
		id = m.active
	}
	if id == "" {
		return nil, errors.New("no debug session is active; start one first")
	}
	session := m.sessions[id]
	if session == nil {
		return nil, fmt.Errorf("debug session %q not found", id)
	}
	return session, nil
}

// ActiveSession returns the most recently started session, if any. The
// returned Session owns its own synchronization and remains valid until the
// manager is closed.
func (m *Manager) ActiveSession() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.active == "" {
		return nil
	}
	return m.sessions[m.active]
}

func (m *Manager) Sessions() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Status, 0, len(m.sessions))
	for _, session := range m.sessions {
		result = append(result, session.Status())
	}
	slices.SortFunc(result, func(a, b Status) int { return a.StartedAt.Compare(b.StartedAt) })
	return result
}

// Breakpoints returns the editor-owned breakpoint set. These breakpoints are
// applied automatically to subsequently launched sessions.
func (m *Manager) Breakpoints(path string) []SourceBreakpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.breakpoints[filepath.Clean(path)])
}

func (m *Manager) SetBreakpoints(ctx context.Context, path string, values []SourceBreakpoint) ([]Breakpoint, error) {
	path = filepath.Clean(path)
	m.mu.Lock()
	m.breakpoints[path] = slices.Clone(values)
	if len(values) == 0 {
		delete(m.breakpoints, path)
	}
	session := m.sessions[m.active]
	m.mu.Unlock()
	if session == nil || session.Status().State == StateTerminated {
		return nil, nil
	}
	return session.SetBreakpoints(ctx, path, values)
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	session, err := m.Session(id)
	if err != nil {
		return err
	}
	status := session.Status()
	if err := session.Disconnect(ctx, true); err != nil && status.State != StateTerminated {
		return err
	}
	m.mu.Lock()
	delete(m.sessions, status.SessionID)
	if m.active == status.SessionID {
		m.active = ""
		var newest time.Time
		for candidateID, candidate := range m.sessions {
			started := candidate.Status().StartedAt
			if started.After(newest) {
				newest = started
				m.active = candidateID
			}
		}
	}
	m.mu.Unlock()
	return nil
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
	m.active = ""
	m.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

func resolveAdapterCommand(command string) string {
	if filepath.IsAbs(command) {
		if executableFile(command) {
			return command
		}
		return ""
	}
	if path, err := exec.LookPath(command); err == nil {
		return path
	}
	var dirs []string
	if value := os.Getenv("GOBIN"); value != "" {
		dirs = append(dirs, value)
	}
	if value := os.Getenv("GOPATH"); value != "" {
		dirs = append(dirs, filepath.Join(value, "bin"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".local", "share", "mise", "shims"),
			filepath.Join(home, ".asdf", "shims"),
		)
	}
	if runtime.GOOS != "windows" {
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin")
	}
	for _, dir := range dirs {
		for _, name := range commandNames(command) {
			path := filepath.Join(dir, name)
			if executableFile(path) {
				return path
			}
		}
	}
	return ""
}

func commandNames(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{command + ".exe", command + ".cmd", command + ".bat", command}
	}
	return []string{command}
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode()&0o111 != 0
}
