// Package devtools installs and resolves language servers and debug adapters
// managed by Wingman. Managed tools live directly below WINGMAN_HOME/tools;
// each tool has one active installation which is replaced on update.
package devtools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

const (
	statusName          = ".wingman"
	updateLockName      = ".update-lock"
	updateInterval      = 24 * time.Hour
	retryInterval       = time.Hour
	installTimeout      = 10 * time.Minute
	versionProbeTimeout = 5 * time.Second
	staleLockAge        = 30 * time.Minute
	lockHeartbeat       = time.Minute
)

type installerKind string

const (
	installerGo         installerKind = "go"
	installerRust       installerKind = "rust"
	installerNPM        installerKind = "npm"
	installerPython     installerKind = "python"
	installerDotnet     installerKind = "dotnet"
	installerJavaScript installerKind = "javascript"
	installerBrowser    installerKind = "browser"
	installerCodeLLDB   installerKind = "codelldb"
	installerNetCoreDbg installerKind = "netcoredbg"
	installerJava       installerKind = "java"
)

// Requirement lists equivalent commands in preference order. Project and
// standard system commands satisfy the requirement before a managed recipe is
// selected. ManagedOnly is reserved for capabilities that need bundled files
// in addition to an executable, such as Java debugging.
type Requirement struct {
	Alternatives         []string
	Workspace            string
	Projects             []string
	MinimumMajorVersions map[string]int
	ManagedOnly          bool
}

// Progress identifies the managed tool currently being checked or installed.
type Progress struct {
	Tool    string
	Label   string
	Current int
	Total   int
}

type recipe struct {
	ID         string
	Label      string
	Kind       installerKind
	Packages   []string
	Commands   []string
	WorkingDir string
}

// UnavailableError means no usable copy of a required tool remains after an
// automatic installation failure. Refresh failures for an existing copy do
// not use this type.
type UnavailableError struct {
	Tool string
	Err  error
}

func (e *UnavailableError) Error() string { return fmt.Sprintf("%s is unavailable: %v", e.Tool, e.Err) }
func (e *UnavailableError) Unwrap() error { return e.Err }

func IsUnavailable(err error) bool {
	var unavailable *UnavailableError
	return errors.As(err, &unavailable)
}

// UnavailableTools returns the stable tool IDs from a possibly joined update
// error. Callers can present a concise notice without exposing installer logs.
func UnavailableTools(err error) []string {
	seen := make(map[string]bool)
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if unavailable, ok := err.(*UnavailableError); ok && unavailable.Tool != "" {
			seen[unavailable.Tool] = true
		}
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(wrapped.Unwrap())
		}
	}
	visit(err)
	tools := make([]string, 0, len(seen))
	for tool := range seen {
		tools = append(tools, tool)
	}
	slices.Sort(tools)
	return tools
}

// The catalog is deliberately small and deterministic. Ecosystem-specific
// recipes live beside their installers; SDKs and compilers remain external.
var catalog = allRecipes()

// ToolLabel returns the user-facing name stored with a managed tool's recipe.
// Unknown IDs are returned unchanged so callers always have useful text.
func ToolLabel(id string) string {
	for _, item := range catalog {
		if item.ID == id {
			return item.Label
		}
	}
	return id
}

// ToolLabels maps stable catalog IDs to their user-facing names.
func ToolLabels(ids []string) []string {
	labels := make([]string, len(ids))
	for index, id := range ids {
		labels[index] = ToolLabel(id)
	}
	return labels
}

func allRecipes() []recipe {
	groups := [][]recipe{goRecipes, rustRecipes, dotnetRecipes, nodeRecipes, pythonRecipes, javascriptRecipes, javaRecipes}
	var result []recipe
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

type commandRunner func(context.Context, string, []string, string, []string) ([]byte, error)
type recipeInstaller func(context.Context, recipe, string) error
type fetcher func(context.Context, string) ([]byte, error)

// Manager owns the latest-only managed tool directory.
type Manager struct {
	root    string
	now     func() time.Time
	look    func(string) (string, error)
	run     commandRunner
	fetch   fetcher
	install recipeInstaller

	byCommand map[string]recipe
	updates   chan struct{}
}

// New creates a manager rooted at WINGMAN_HOME/tools.
func New() (*Manager, error) {
	root, err := layout.WingmanPath("tools")
	if err != nil {
		return nil, err
	}
	manager := newManager(root)
	manager.install = manager.installRecipe
	manager.activatePendingUpdates()
	return manager, nil
}

func newManager(root string) *Manager {
	manager := &Manager{
		root:      filepath.Clean(root),
		now:       time.Now,
		look:      tooling.LookPath,
		run:       runCommand,
		fetch:     fetchURL,
		byCommand: make(map[string]recipe),
		updates:   make(chan struct{}, 1),
	}
	for _, item := range catalog {
		for _, command := range item.Commands {
			manager.byCommand[command] = item
		}
	}
	return manager
}

// Root returns the directory containing managed tools.
func (m *Manager) Root() string { return m.root }

// CanManage reports whether command has a curated latest-version recipe.
func (m *Manager) CanManage(command string) bool {
	_, ok := m.byCommand[command]
	return ok
}

// Resolve returns the absolute path of a managed command, if installed.
func (m *Manager) Resolve(command string) string {
	item, ok := m.byCommand[command]
	if !ok {
		return ""
	}
	return resolveInstalledCommand(filepath.Join(m.root, item.ID), command)
}

// Update installs the latest release for every selected requirement. A fresh
// installation is checked at most once per day; failed checks are retried
// after a short backoff. Successful updates replace the prior directory and
// remove it rather than accumulating versions.
func (m *Manager) Update(ctx context.Context, requirements []Requirement, progress ...func(Progress)) (bool, error) {
	select {
	case m.updates <- struct{}{}:
		defer func() { <-m.updates }()
	case <-ctx.Done():
		return false, ctx.Err()
	}

	selected := make(map[string]recipe)
	for _, requirement := range requirements {
		if !requirement.ManagedOnly && m.externalAvailable(ctx, requirement) {
			continue
		}
		for _, command := range requirement.Alternatives {
			if item, ok := m.byCommand[command]; ok {
				item.WorkingDir = requirementWorkingDir(requirement)
				if current, exists := selected[item.ID]; exists && current.WorkingDir != "" {
					item.WorkingDir = current.WorkingDir
				}
				selected[item.ID] = item
				break
			}
		}
	}

	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return false, nil
	}
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return false, err
	}
	release, err := acquireUpdateLock(ctx, m.root, m.now)
	if err != nil {
		return false, err
	}
	defer release()

	changed := false
	var updateErrors []error
	for index, id := range ids {
		if err := ctx.Err(); err != nil {
			updateErrors = append(updateErrors, err)
			break
		}
		item := selected[id]
		for _, report := range progress {
			if report != nil {
				report(Progress{Tool: item.ID, Label: item.Label, Current: index + 1, Total: len(ids)})
			}
		}
		if err := recoverInterruptedUpdate(m.root, item.ID); err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("recover %s: %w", item.ID, err))
			continue
		}
		if m.fresh(item) {
			continue
		}
		ready := installationReady(item, filepath.Join(m.root, item.ID))
		if m.retryDeferred(item) {
			if !ready {
				updateErrors = append(updateErrors, &UnavailableError{
					Tool: item.ID,
					Err:  fmt.Errorf("automatic installation was attempted recently; Wingman will retry after %s", retryInterval),
				})
			}
			continue
		}
		updated, err := m.updateOne(ctx, item)
		changed = changed || updated
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				updateErrors = append(updateErrors, contextErr)
				break
			}
			retryErr := m.deferRetry(item)
			if !ready {
				err = &UnavailableError{Tool: item.ID, Err: err}
			}
			updateErrors = append(updateErrors, fmt.Errorf("update %s: %w", item.ID, errors.Join(err, retryErr)))
			continue
		}
		_ = os.Remove(m.retryPath(item))
	}
	return changed, errors.Join(updateErrors...)
}

func (m *Manager) externalAvailable(ctx context.Context, requirement Requirement) bool {
	if strings.TrimSpace(requirement.Workspace) == "" {
		return false
	}
	resolver := tooling.Resolver{
		Workspace: requirement.Workspace,
		Lookup: func(command string) string {
			path, err := m.look(command)
			if err != nil {
				return ""
			}
			return path
		},
	}
	projects := requirement.Projects
	if len(projects) == 0 {
		projects = []string{""}
	}
	for _, project := range projects {
		available := false
		projectCandidates := []string(nil)
		if project != "" {
			projectCandidates = []string{project}
		}
		for _, command := range requirement.Alternatives {
			minimum := requirement.MinimumMajorVersions[command]
			for _, candidate := range resolver.Candidates(projectCandidates, command) {
				probeCtx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
				supported := tooling.MajorVersionAtLeast(probeCtx, candidate.Path, minimum)
				cancel()
				if supported {
					available = true
					break
				}
			}
			if available {
				break
			}
		}
		if !available {
			return false
		}
	}
	return true
}

func requirementWorkingDir(requirement Requirement) string {
	workspace := filepath.Clean(requirement.Workspace)
	for _, directory := range append(slices.Clone(requirement.Projects), requirement.Workspace) {
		if directory == "" {
			continue
		}
		directory = filepath.Clean(directory)
		if requirement.Workspace != "" {
			relative, err := filepath.Rel(workspace, directory)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
		}
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			return directory
		}
	}
	return ""
}

func (m *Manager) fresh(item recipe) bool {
	root := filepath.Join(m.root, item.ID)
	if !installationReady(item, root) {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, statusName))
	if err != nil {
		return false
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	age := m.now().Sub(updatedAt)
	return age >= 0 && age < updateInterval
}

func (m *Manager) updateOne(ctx context.Context, item recipe) (bool, error) {
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return false, err
	}
	stage, err := os.MkdirTemp(m.root, "."+item.ID+"-install-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(stage)

	if m.install == nil {
		return false, errors.New("no installer configured")
	}
	installCtx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	if err := m.install(installCtx, item, stage); err != nil {
		return false, err
	}
	if !installationReady(item, stage) {
		return false, errors.New("installer did not provide a complete tool")
	}
	updatedAt := m.now().UTC().Format(time.RFC3339Nano) + "\n"
	if err := os.WriteFile(filepath.Join(stage, statusName), []byte(updatedAt), 0o600); err != nil {
		return false, err
	}
	return replaceDirectory(stage, filepath.Join(m.root, item.ID))
}

func (m *Manager) retryPath(item recipe) string {
	return filepath.Join(m.root, "."+item.ID+"-retry")
}

func (m *Manager) retryDeferred(item recipe) bool {
	info, err := os.Stat(m.retryPath(item))
	if err != nil {
		return false
	}
	age := m.now().Sub(info.ModTime())
	if age >= 0 && age < retryInterval {
		return true
	}
	_ = os.Remove(m.retryPath(item))
	return false
}

func (m *Manager) deferRetry(item recipe) error {
	path := m.retryPath(item)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return fmt.Errorf("record update retry: %w", err)
	}
	stamp := m.now()
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		return fmt.Errorf("record update retry time: %w", err)
	}
	return nil
}

func installWorkingDir(item recipe, fallback string) string {
	if info, err := os.Stat(item.WorkingDir); err == nil && info.IsDir() {
		return item.WorkingDir
	}
	return fallback
}

func installationReady(item recipe, root string) bool {
	for _, command := range item.Commands {
		if resolveInstalledCommand(root, command) == "" {
			return false
		}
	}
	switch item.Kind {
	case installerJavaScript:
		return regularFile(filepath.Join(root, "js-debug", "src", "dapDebugServer.js"))
	case installerBrowser:
		path, err := chromeForTestingExecutable(root, runtime.GOOS, runtime.GOARCH)
		return err == nil && executableFile(path)
	case installerCodeLLDB:
		name := "codelldb"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return executableFile(filepath.Join(root, "extension", "adapter", name))
	case installerNetCoreDbg:
		name := "netcoredbg"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return executableFile(filepath.Join(root, "netcoredbg", name))
	case installerJava:
		return len(javaDebugBundlesAt(root)) > 0
	default:
		return true
	}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (m *Manager) installRecipe(ctx context.Context, item recipe, stage string) error {
	switch item.Kind {
	case installerGo:
		return m.installGo(ctx, item, stage)

	case installerRust:
		return m.installRustAnalyzer(ctx, item, stage)

	case installerNPM:
		return m.installNPM(ctx, item, stage)

	case installerPython:
		return m.installPython(ctx, item, stage)

	case installerDotnet:
		return m.installDotnet(ctx, item, stage)

	case installerJavaScript:
		return m.installJavaScript(ctx, item, stage)

	case installerBrowser:
		return m.installChromeForTesting(ctx, item, stage)

	case installerCodeLLDB:
		return m.installCodeLLDB(ctx, item, stage)

	case installerNetCoreDbg:
		return m.installNetCoreDbg(ctx, item, stage)

	case installerJava:
		return m.installJava(ctx, item, stage)

	default:
		return fmt.Errorf("unsupported installer %q", item.Kind)
	}
}

func resolveInstalledCommand(root, command string) string {
	directories := []string{
		filepath.Join(root, "bin"),
		filepath.Join(root, "Scripts"),
		filepath.Join(root, "node_modules", ".bin"),
	}
	for _, directory := range directories {
		for _, name := range commandNames(command) {
			candidate := filepath.Join(directory, name)
			if runnableFile(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func commandNames(command string) []string {
	return tooling.Candidates(runtime.GOOS, command)
}

func executableFile(path string) bool {
	return tooling.Executable(path)
}

func runnableFile(path string) bool {
	return tooling.Runnable(path)
}

// replaceDirectory reports whether stage became the active installation. An
// error after activation only means the previous directory could not yet be
// removed; recovery retries that cleanup on the next update.
func replaceDirectory(stage, target string) (bool, error) {
	backup := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-old-"+uuid.NewString())
	hadTarget := false
	if _, err := os.Stat(target); err == nil {
		hadTarget = true
		if err := os.Rename(target, backup); err != nil {
			pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-pending")
			if filepath.Clean(stage) == filepath.Clean(pending) {
				return false, err
			}
			if removeErr := os.RemoveAll(pending); removeErr != nil {
				return false, errors.Join(err, removeErr)
			}
			if pendingErr := os.Rename(stage, pending); pendingErr != nil {
				return false, errors.Join(err, pendingErr)
			}
			return false, fmt.Errorf("update is ready and will activate the next time Wingman starts: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.Rename(stage, target); err != nil {
		if hadTarget {
			_ = os.Rename(backup, target)
		}
		return false, err
	}
	if hadTarget {
		if err := os.RemoveAll(backup); err != nil {
			return true, fmt.Errorf("remove previous installation: %w", err)
		}
	}
	return true, nil
}

func (m *Manager) activatePendingUpdates() {
	if _, err := os.Stat(m.root); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := acquireUpdateLock(ctx, m.root, m.now)
	if err != nil {
		return
	}
	defer release()
	for _, item := range catalog {
		pending := filepath.Join(m.root, "."+item.ID+"-pending")
		if _, err := os.Stat(pending); err != nil {
			continue
		}
		_, _ = replaceDirectory(pending, filepath.Join(m.root, item.ID))
	}
}

func recoverInterruptedUpdate(root, id string) error {
	target := filepath.Join(root, id)
	backups, err := pathsWithPrefix(root, "."+id+"-old-")
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(target); os.IsNotExist(statErr) && len(backups) > 0 {
		latest := ""
		latestTime := time.Time{}
		for _, candidate := range backups {
			info, err := os.Stat(candidate)
			if err != nil {
				return err
			}
			if latest == "" || info.ModTime().After(latestTime) {
				latest = candidate
				latestTime = info.ModTime()
			}
		}
		if err := os.Rename(latest, target); err != nil {
			return err
		}
		backups = slices.DeleteFunc(backups, func(path string) bool { return path == latest })
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	for _, path := range backups {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	stages, err := pathsWithPrefix(root, "."+id+"-install-")
	if err != nil {
		return err
	}
	for _, path := range stages {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func pathsWithPrefix(root, prefix string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			paths = append(paths, filepath.Join(root, entry.Name()))
		}
	}
	slices.Sort(paths)
	return paths, nil
}

func acquireUpdateLock(ctx context.Context, root string, now func() time.Time) (func(), error) {
	path := filepath.Join(root, updateLockName)
	token := uuid.NewString()
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, writeErr := file.WriteString(token); writeErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, writeErr
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, closeErr
			}
			return maintainUpdateLock(path, token, now), nil
		}
		if !os.IsExist(err) {
			return nil, err
		}

		info, statErr := os.Stat(path)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return nil, statErr
		}
		if now().Sub(info.ModTime()) > staleLockAge {
			stale := path + "-stale-" + uuid.NewString()
			if err := os.Rename(path, stale); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return nil, err
			}
			if err := os.RemoveAll(stale); err != nil {
				return nil, err
			}
			continue
		}

		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func maintainUpdateLock(path, token string, now func() time.Time) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(lockHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !ownsUpdateLock(path, token) {
					return
				}
				stamp := now()
				_ = os.Chtimes(path, stamp, stamp)
			case <-stop:
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			if ownsUpdateLock(path, token) {
				_ = os.Remove(path)
			}
		})
	}
}

func ownsUpdateLock(path, token string) bool {
	data, err := os.ReadFile(path)
	return err == nil && string(data) == token
}

func runCommand(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Env = tooling.Environment(name, env)
	command.WaitDelay = 3 * time.Second
	return command.CombinedOutput()
}

func commandError(output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}
	const max = 4 << 10
	if len(message) > max {
		message = message[len(message)-max:]
	}
	return fmt.Errorf("%w: %s", err, message)
}
