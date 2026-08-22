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

var ErrActiveSession = errors.New("a debug session is already active")

type detectedAdapter struct {
	adapter  AdapterDescriptor
	projects []string
}

type Manager struct {
	root      string
	adapters  []AdapterDescriptor
	lookup    func(string) string
	managed   func(string) string
	start     sessionStarter
	terminal  TerminalLauncher
	connector AdapterConnector
	startMu   sync.Mutex

	detectMu   sync.Mutex
	detected   []detectedAdapter
	detectedAt time.Time

	mu          sync.Mutex
	session     *Session
	breakpoints map[string][]SourceBreakpoint
	closed      bool
}

type sessionStarter func(context.Context, string, Plan, StartOptions) (*Session, error)

func NewManager(root string, adapters ...AdapterDescriptor) *Manager {
	return newManager(root, adapters, resolveAdapterCommand, startSession)
}

// SetTerminalLauncher connects the protocol client to the editor's PTY host.
// Passing nil disables terminal launches while leaving captured-output
// sessions available.
func (m *Manager) SetTerminalLauncher(launcher TerminalLauncher) {
	m.mu.Lock()
	m.terminal = launcher
	m.mu.Unlock()
}

// SetAdapterConnector installs the host bridge used by adapters such as Java,
// whose DAP socket is created by a language server rather than an executable.
func (m *Manager) SetAdapterConnector(connector AdapterConnector) {
	m.mu.Lock()
	m.connector = connector
	m.mu.Unlock()
}

// SetCommandResolver checks an application-managed tool location before the
// host PATH and workspace-local adapter locations.
func (m *Manager) SetCommandResolver(resolve func(string) string) {
	m.detectMu.Lock()
	m.managed = resolve
	m.detectedAt = time.Time{}
	m.detectMu.Unlock()
}

// SetAdapters replaces descriptors used by future discovery and sessions.
// An already-running session is unaffected.
func (m *Manager) SetAdapters(adapters ...AdapterDescriptor) {
	m.detectMu.Lock()
	m.adapters = cloneAdapters(adapters)
	m.detected = nil
	m.detectedAt = time.Time{}
	m.detectMu.Unlock()
}

// InvalidateDetection makes newly installed or updated adapters visible on
// the next lookup without waiting for the detection cache TTL.
func (m *Manager) InvalidateDetection() {
	m.detectMu.Lock()
	m.detectedAt = time.Time{}
	m.detectMu.Unlock()
}

func newManager(root string, adapters []AdapterDescriptor, lookup func(string) string, starter sessionStarter) *Manager {
	return &Manager{
		root:        filepath.Clean(root),
		adapters:    cloneAdapters(adapters),
		lookup:      lookup,
		start:       starter,
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
		projects, err := detectProjects(ctx, m.root, candidate.Markers, candidate.SourceExtensions)
		if err != nil {
			return nil, fmt.Errorf("detect %s projects: %w", candidate.Language, err)
		}
		if len(projects) == 0 {
			continue
		}
		command := m.resolveDetectedCommand(candidate.Command, projects)
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

func cloneAdapters(values []AdapterDescriptor) []AdapterDescriptor {
	cloned := make([]AdapterDescriptor, len(values))
	for i, value := range values {
		cloned[i] = value
		cloned[i].Args = slices.Clone(value.Args)
		cloned[i].Markers = slices.Clone(value.Markers)
		cloned[i].SourceExtensions = slices.Clone(value.SourceExtensions)
		cloned[i].Defaults = maps.Clone(value.Defaults)
		cloned[i].ConfigurationPaths = slices.Clone(value.ConfigurationPaths)
		cloned[i].IOValues = maps.Clone(value.IOValues)
	}
	return cloned
}

func (m *Manager) resolveDetectedCommand(command string, projects []string) string {
	if command == "" {
		return ""
	}
	if m.managed != nil {
		if resolved := m.managed(command); resolved != "" {
			return resolved
		}
	}
	for _, project := range projects {
		if resolved := resolveProjectAdapterCommand(project, m.root, command); resolved != "" {
			return resolved
		}
	}
	if m.lookup != nil {
		return m.lookup(command)
	}
	return ""
}

func cloneDetected(values []detectedAdapter) []detectedAdapter {
	cloned := make([]detectedAdapter, len(values))
	for i, value := range values {
		cloned[i] = value
		cloned[i].adapter = cloneAdapters([]AdapterDescriptor{value.adapter})[0]
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
			IOConfigKey:        value.adapter.IOConfigKey,
			TerminalStrategy:   value.adapter.TerminalStrategy,
		})
	}
	return result, nil
}

func (m *Manager) Start(ctx context.Context, options StartOptions) (*Session, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("DAP manager is closed")
	}
	if m.session != nil && m.session.Status().State != StateTerminated {
		m.mu.Unlock()
		return nil, ErrActiveSession
	}
	combinedBreakpoints := make(map[string][]SourceBreakpoint, len(options.Breakpoints)+len(m.breakpoints))
	for path, breakpoints := range options.Breakpoints {
		combinedBreakpoints[filepath.Clean(path)] = slices.Clone(breakpoints)
	}
	for path, breakpoints := range m.breakpoints {
		path = filepath.Clean(path)
		combinedBreakpoints[path] = mergeSourceBreakpoints(combinedBreakpoints[path], breakpoints)
	}
	options.Breakpoints = combinedBreakpoints
	options.terminalLauncher = m.terminal
	options.adapterConnector = m.connector
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
	if registered := m.registeredAdapter(selected.adapter.Name); registered != nil {
		command := m.resolveProjectCommand(registered.Command, plan.ProjectDir)
		if command == "" {
			return nil, fmt.Errorf("debug adapter %s is not available for project %s", registered.Name, plan.ProjectDir)
		}
		plan.Adapter.Command = command
		plan.Adapter.Args = slices.Clone(registered.Args)
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
	previous := m.session
	m.session = session
	m.mu.Unlock()
	if previous != nil {
		previous.Close()
	}
	return session, nil
}

func (m *Manager) registeredAdapter(name string) *AdapterDescriptor {
	for _, adapter := range m.adapters {
		if strings.EqualFold(adapter.Name, name) {
			value := cloneAdapters([]AdapterDescriptor{adapter})[0]
			return &value
		}
	}
	return nil
}

func (m *Manager) resolveProjectCommand(command, project string) string {
	if m.managed != nil {
		if resolved := m.managed(command); resolved != "" {
			return resolved
		}
	}
	if resolved := resolveProjectAdapterCommand(project, m.root, command); resolved != "" {
		return resolved
	}
	if m.lookup != nil {
		return m.lookup(command)
	}
	return ""
}

// mergeSourceBreakpoints keeps a plan's initial stops and folds in the
// editor-owned set. At the same source position, the user's gutter breakpoint
// wins so its condition or log message is preserved.
func mergeSourceBreakpoints(planned, editor []SourceBreakpoint) []SourceBreakpoint {
	result := slices.Clone(planned)
	positions := make(map[[2]int]int, len(result))
	for index, breakpoint := range result {
		positions[[2]int{breakpoint.Line, breakpoint.Column}] = index
	}
	for _, breakpoint := range editor {
		position := [2]int{breakpoint.Line, breakpoint.Column}
		if index, exists := positions[position]; exists {
			result[index] = breakpoint
			continue
		}
		positions[position] = len(result)
		result = append(result, breakpoint)
	}
	slices.SortFunc(result, func(left, right SourceBreakpoint) int {
		if left.Line != right.Line {
			return left.Line - right.Line
		}
		return left.Column - right.Column
	})
	return result
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
	ioMode := options.IO
	if ioMode == "" {
		ioMode = IOOutput
	}
	if ioMode != IOOutput && ioMode != IOTerminal {
		return Plan{}, fmt.Errorf("I/O mode must be %s or %s (got %q)", IOOutput, IOTerminal, options.IO)
	}
	if ioMode == IOTerminal && selected.adapter.TerminalStrategy == TerminalUnsupported {
		return Plan{}, fmt.Errorf("debug adapter %s does not support a terminal", selected.adapter.Name)
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
		arguments["name"] = "Wingman debug"
	}
	arguments["request"] = requestName
	if selected.adapter.IOConfigKey != "" {
		value := string(ioMode)
		if mapped := selected.adapter.IOValues[ioMode]; mapped != "" {
			value = mapped
		}
		arguments[selected.adapter.IOConfigKey] = value
	}

	targetKey := selected.adapter.TargetConfigKey
	if targetKey == "" {
		targetKey = "program"
	}
	target, _ := arguments[targetKey].(string)
	mode, _ := arguments["mode"].(string)
	return Plan{
		Adapter:    selected.adapter,
		ProjectDir: projectDir,
		Target:     target,
		Mode:       mode,
		Request:    requestName,
		IO:         ioMode,
		Arguments:  arguments,
	}, nil
}

// ResolveConfigurationPaths validates and resolves the path fields declared by
// an adapter descriptor. All other adapter configuration remains opaque.
func ResolveConfigurationPaths(workspace, projectDir string, fields []ConfigurationPath, configuration map[string]any) (map[string]any, error) {
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	if !filepath.IsAbs(projectDir) {
		projectDir = filepath.Join(workspace, projectDir)
	}
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
		info, statErr := os.Stat(path)
		if statErr != nil && (!field.AllowMissing || !errors.Is(statErr, os.ErrNotExist)) {
			return nil, fmt.Errorf("configuration %s path %q does not exist", field.Key, value)
		}
		if err := ensurePotentialPathInside(workspace, path, field.AllowMissing); err != nil {
			return nil, fmt.Errorf("configuration %s path %q: %w", field.Key, value, err)
		}
		if statErr == nil && field.Directory && !info.IsDir() {
			return nil, fmt.Errorf("configuration %s path %q must be a directory", field.Key, value)
		}
		resolved[field.Key] = filepath.Clean(path)
	}
	return resolved, nil
}

func ensurePotentialPathInside(workspace, path string, allowMissing bool) error {
	if !allowMissing {
		return ensureResolvedPathInside(workspace, path)
	}
	probe := path
	for {
		if _, err := os.Lstat(probe); err == nil {
			if err := ensureResolvedPathInside(workspace, probe); err != nil {
				return err
			}
			if probe != path {
				info, err := os.Stat(probe)
				if err != nil {
					return fmt.Errorf("inspect existing parent: %w", err)
				}
				if !info.IsDir() {
					return errors.New("existing parent is not a directory")
				}
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect path: %w", err)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return errors.New("no existing parent directory")
		}
		probe = parent
	}
}

func selectProjectDir(workspace string, projects []string, requested string, configuration map[string]any) (string, error) {
	if strings.TrimSpace(requested) != "" {
		path, err := ResolveWorkspaceDirectory(workspace, requested)
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

func ResolveWorkspaceDirectory(workspace, value string) (string, error) {
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
	if err := ensureResolvedPathInside(workspace, abs); err != nil {
		return "", fmt.Errorf("project_dir %q: %w", value, err)
	}
	return filepath.Clean(abs), nil
}

func ensureResolvedPathInside(workspace, path string) error {
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace symlinks: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}
	rel, err := filepath.Rel(resolvedWorkspace, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("must stay inside the workspace after resolving symlinks")
	}
	return nil
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
	if m.session == nil {
		return nil, errors.New("no debug session is active; start one first")
	}
	if id != "" && id != m.session.ID() {
		return nil, fmt.Errorf("debug session %q not found", id)
	}
	return m.session, nil
}

// ActiveSession returns the most recently started session, if any. The
// returned Session owns its own synchronization and remains valid until the
// manager is closed.
func (m *Manager) ActiveSession() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	return m.session
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
	session := m.session
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
	disconnectErr := session.Disconnect(ctx, true)
	m.mu.Lock()
	if m.session == session {
		m.session = nil
	}
	m.mu.Unlock()
	if disconnectErr != nil && status.State != StateTerminated {
		return disconnectErr
	}
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	session := m.session
	m.session = nil
	m.mu.Unlock()
	if session != nil {
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
	if value := os.Getenv("PNPM_HOME"); value != "" {
		dirs = append(dirs, value)
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".dotnet", "tools"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".deno", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".local", "share", "mise", "shims"),
			filepath.Join(home, ".npm-global", "bin"),
		)
		if runtime.GOOS == "windows" {
			dirs = append(dirs, filepath.Join(home, "scoop", "shims"))
			if appData := os.Getenv("APPDATA"); appData != "" {
				dirs = append(dirs, filepath.Join(appData, "npm"))
			}
			if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
				dirs = append(dirs,
					filepath.Join(localAppData, "nvim-data", "mason", "bin"),
					filepath.Join(localAppData, "pnpm"),
					filepath.Join(localAppData, "Volta", "bin"),
					filepath.Join(localAppData, "Microsoft", "WinGet", "Links"),
				)
			}
			if programData := os.Getenv("PROGRAMDATA"); programData != "" {
				dirs = append(dirs, filepath.Join(programData, "chocolatey", "bin"))
			}
		} else {
			dirs = append(dirs,
				filepath.Join(home, ".local", "share", "nvim", "mason", "bin"),
				filepath.Join(home, "Library", "pnpm"),
				filepath.Join(home, ".local", "share", "pnpm"),
			)
		}
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

func resolveProjectAdapterCommand(project, workspace, command string) string {
	project = filepath.Clean(project)
	workspace = filepath.Clean(workspace)
	rel, err := filepath.Rel(workspace, project)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	for {
		if resolved := resolveLocalAdapterCommand(project, command); resolved != "" {
			return resolved
		}
		if project == workspace {
			return ""
		}
		parent := filepath.Dir(project)
		if parent == project {
			return ""
		}
		project = parent
	}
}

func resolveLocalAdapterCommand(project, command string) string {
	directories := []string{
		filepath.Join("node_modules", ".bin"),
		filepath.Join(".venv", "bin"), filepath.Join("venv", "bin"), filepath.Join("env", "bin"),
		filepath.Join(".venv", "Scripts"), filepath.Join("venv", "Scripts"), filepath.Join("env", "Scripts"),
		filepath.Join("vendor", "bin"),
	}
	for _, directory := range directories {
		for _, name := range commandNames(command) {
			path := filepath.Join(project, directory, name)
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
