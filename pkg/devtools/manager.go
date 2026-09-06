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

	"github.com/adrianliechti/wingman-agent/internal/process"
	"github.com/adrianliechti/wingman-agent/internal/tooling"
	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

const (
	statusName          = ".status"
	lockName            = ".lock"
	updateInterval      = 24 * time.Hour
	retryInterval       = time.Hour
	installTimeout      = 10 * time.Minute
	versionProbeTimeout = 5 * time.Second
	staleLockAge        = 30 * time.Minute
	lockHeartbeat       = time.Minute
)

type installerKind string

const (
	installerGo     installerKind = "go"
	installerRustup installerKind = "rustup"
	installerNPM    installerKind = "npm"
	installerPython installerKind = "python"
	installerDotnet installerKind = "dotnet"
	installerMaven  installerKind = "maven"
	installerGitHub installerKind = "github"
)

// Requirement lists equivalent commands in preference order. The first
// command with a managed recipe selects the package to install or update.
type Requirement struct {
	Alternatives         []string
	Workspace            string
	Projects             []string
	MinimumMajorVersions map[string]int
}

// ProgressPhase describes what the managed-tool updater is doing. Checking is
// intentionally distinct from installation so status surfaces do not imply
// that a fresh installation is being replaced on every startup.
type ProgressPhase string

const (
	ProgressChecking   ProgressPhase = "checking"
	ProgressInstalling ProgressPhase = "installing"
	ProgressUpdating   ProgressPhase = "updating"
)

// Progress identifies the managed tool currently being checked, installed, or
// updated.
type Progress struct {
	Tool    string        `json:"tool"`
	Label   string        `json:"label"`
	Phase   ProgressPhase `json:"phase"`
	Current int           `json:"current"`
	Total   int           `json:"total"`
}

// ToolStatus describes an installation without downloading or changing tools.
type ToolStatus struct {
	Tool        string `json:"tool"`
	Label       string `json:"label"`
	Installed   bool   `json:"installed"`
	Installable bool   `json:"installable"`
}

type recipe struct {
	ID          string
	Label       string
	Kind        installerKind
	Packages    []string
	Commands    []string
	WorkingDirs []string
}

type selectedTool struct {
	recipe
	requirements []Requirement
}

// ready validates both the installation and its project-specific version
// requirements. It works for the active directory and for a staged update.
func (s selectedTool) ready(ctx context.Context, root string) bool {
	if !installationReady(s.recipe, root) {
		return false
	}
	resolve := func(command string) string {
		if !slices.Contains(s.Commands, command) {
			return ""
		}
		return resolveInstalledCommand(root, command)
	}
	for _, requirement := range s.requirements {
		if !requirement.installed(ctx, resolve) {
			return false
		}
	}
	return true
}

// ErrInstallRequired means an update-only request encountered a missing tool.
var ErrInstallRequired = errors.New("managed tool installation requires confirmation")

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
	return slices.Concat(goRecipes, rustRecipes, dotnetRecipes, nodeRecipes, pythonRecipes, javaRecipes, githubRecipes)
}

type commandRunner func(context.Context, string, []string, string, []string) ([]byte, error)

// recipeInstaller returns the upstream version marker recorded in the status
// file; installers whose package manager owns versioning return "".
type recipeInstaller func(context.Context, recipe, string) (string, error)
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
	if m == nil {
		return false
	}
	_, ok := m.byCommand[command]
	return ok
}

// ToolDir returns a complete managed installation for adapters that need
// bundled files in addition to an executable availability token.
func (m *Manager) ToolDir(id string) string {
	if m == nil {
		return ""
	}
	root := filepath.Join(m.root, filepath.Base(id))
	for _, item := range catalog {
		if item.ID == id && installationReady(item, root) {
			return root
		}
	}
	return ""
}

// Resolve returns a command only from a complete managed installation.
func (m *Manager) Resolve(command string) string {
	if m == nil {
		return ""
	}
	item, ok := m.byCommand[command]
	if !ok {
		return ""
	}
	root := filepath.Join(m.root, item.ID)
	if !installationReady(item, root) {
		return ""
	}
	return resolveInstalledCommand(root, command)
}

// Status uses the same managed-only resolution as discovery and launch.
func (m *Manager) Status(ctx context.Context, requirements []Requirement) ([]ToolStatus, error) {
	statuses := make(map[string]ToolStatus)
	for _, requirement := range requirements {
		status := ToolStatus{Tool: strings.Join(requirement.Alternatives, " or ")}
		status.Label = status.Tool
		if m != nil {
			if item, ok := m.managedRecipe(requirement); ok {
				status.Tool, status.Label = item.ID, item.Label
				status.Installable = !Disabled()
			}
		}
		status.Installed = requirement.installed(ctx, m.Resolve)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if previous, exists := statuses[status.Tool]; exists {
			status.Installed = previous.Installed && status.Installed
		}
		statuses[status.Tool] = status
	}
	result := make([]ToolStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, status)
	}
	slices.SortFunc(result, func(a, b ToolStatus) int { return strings.Compare(a.Tool, b.Tool) })
	return result, nil
}

func (requirement Requirement) installed(ctx context.Context, resolve func(string) string) bool {
	projects := requirement.Projects
	if len(projects) == 0 {
		projects = []string{requirement.Workspace}
	}
	for _, project := range projects {
		available := false
		for _, command := range requirement.Alternatives {
			path := resolve(command)
			if path == "" {
				continue
			}
			probeCtx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
			available = tooling.MajorVersionAtLeast(probeCtx, path, requirement.MinimumMajorVersions[command], project)
			cancel()
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

// Update installs the latest release for every selected requirement. A fresh
// installation is checked at most once per day; failed checks are retried
// after a short backoff. Successful updates replace the prior directory and
// remove it rather than accumulating versions.
func (m *Manager) Update(ctx context.Context, requirements []Requirement, progress ...func(Progress)) (bool, error) {
	return m.update(ctx, requirements, true, false, progress...)
}

// UpdateOnDemand checks for updates during an explicit user action. It keeps
// the daily refresh interval. Missing tools require install=true, which also
// allows immediate retries after a failed installation.
func (m *Manager) UpdateOnDemand(ctx context.Context, requirements []Requirement, install bool, progress ...func(Progress)) (bool, error) {
	return m.update(ctx, requirements, install, true, progress...)
}

func (m *Manager) update(ctx context.Context, requirements []Requirement, allowInstall, onDemand bool, progress ...func(Progress)) (bool, error) {
	if Disabled() {
		return false, nil
	}
	select {
	case m.updates <- struct{}{}:
		defer func() { <-m.updates }()
	case <-ctx.Done():
		return false, ctx.Err()
	}

	selected := make(map[string]selectedTool)
	for _, requirement := range requirements {
		item, managed := m.managedRecipe(requirement)
		if !managed {
			continue
		}
		current, exists := selected[item.ID]
		if !exists {
			current.recipe = item
		}
		current.WorkingDirs = appendUniquePaths(current.WorkingDirs, requirementWorkingDirs(requirement)...)
		current.requirements = append(current.requirements, requirement)
		selected[item.ID] = current
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
		reportProgress(progress, item.recipe, ProgressChecking, index+1, len(ids))
		if err := recoverInterruptedUpdate(m.root, item.ID); err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("recover %s: %w", item.ID, err))
			continue
		}
		root := filepath.Join(m.root, item.ID)
		ready := item.ready(ctx, root)
		if !ready && !allowInstall {
			updateErrors = append(updateErrors, fmt.Errorf("%s: %w", item.Label, ErrInstallRequired))
			continue
		}
		if ready && m.fresh(item.recipe) {
			continue
		}
		if (!onDemand || ready) && m.retryDeferred(item.recipe) {
			if !ready {
				updateErrors = append(updateErrors, &UnavailableError{
					Tool: item.ID,
					Err:  errors.New("automatic installation was attempted recently; Wingman will retry in about an hour"),
				})
			}
			continue
		}
		phase := ProgressInstalling
		if ready {
			phase = ProgressUpdating
		}
		reportProgress(progress, item.recipe, phase, index+1, len(ids))
		updated, err := m.updateOne(ctx, item)
		available := item.ready(ctx, root)
		if err == nil && !ready {
			// Installing a rustup component can repair an existing launcher
			// without replacing its files. Discovery must still be refreshed.
			updated = true
		}
		changed = changed || updated
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				updateErrors = append(updateErrors, contextErr)
				break
			}
			var retryErr error
			if !updated {
				retryErr = m.deferRetry(item.recipe)
			}
			if !available {
				err = &UnavailableError{Tool: item.ID, Err: err}
			}
			updateErrors = append(updateErrors, fmt.Errorf("update %s: %w", item.ID, errors.Join(err, retryErr)))
			continue
		}
		_ = os.Remove(m.retryPath(item.recipe))
	}
	return changed, errors.Join(updateErrors...)
}

func reportProgress(reports []func(Progress), item recipe, phase ProgressPhase, current, total int) {
	update := Progress{Tool: item.ID, Label: item.Label, Phase: phase, Current: current, Total: total}
	for _, report := range reports {
		if report != nil {
			report(update)
		}
	}
}

func (m *Manager) managedRecipe(requirement Requirement) (recipe, bool) {
	for _, command := range requirement.Alternatives {
		if item, ok := m.byCommand[command]; ok {
			return item, true
		}
	}
	return recipe{}, false
}

func requirementWorkingDirs(requirement Requirement) []string {
	workspace := filepath.Clean(requirement.Workspace)
	directories := requirement.Projects
	if len(directories) == 0 {
		directories = []string{requirement.Workspace}
	}
	var result []string
	for _, directory := range directories {
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
			result = appendUniquePaths(result, directory)
		}
	}
	return result
}

func appendUniquePaths(paths []string, additions ...string) []string {
	for _, addition := range additions {
		if addition == "" {
			continue
		}
		addition = filepath.Clean(addition)
		if !slices.Contains(paths, addition) {
			paths = append(paths, addition)
		}
	}
	return paths
}

// The status file holds the refresh timestamp on its first line and an
// optional upstream version marker on its second.
func readStatus(root string) (updatedAt time.Time, version string, err error) {
	data, err := os.ReadFile(filepath.Join(root, statusName))
	if err != nil {
		return time.Time{}, "", err
	}
	stampLine, versionLine, _ := strings.Cut(string(data), "\n")
	updatedAt, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(stampLine))
	if err != nil {
		return time.Time{}, "", err
	}
	return updatedAt, strings.TrimSpace(versionLine), nil
}

func (m *Manager) writeStatus(root, version string) error {
	content := m.now().UTC().Format(time.RFC3339Nano) + "\n"
	if version != "" {
		content += version + "\n"
	}
	return os.WriteFile(filepath.Join(root, statusName), []byte(content), 0o600)
}

func (m *Manager) fresh(item recipe) bool {
	root := filepath.Join(m.root, item.ID)
	if !installationReady(item, root) {
		return false
	}
	updatedAt, _, err := readStatus(root)
	if err != nil {
		return false
	}
	age := m.now().Sub(updatedAt)
	return age >= 0 && age < updateInterval
}

func (m *Manager) updateOne(ctx context.Context, item selectedTool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
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
	version, err := m.install(installCtx, item.recipe, stage)
	if contextErr := installCtx.Err(); contextErr != nil {
		return false, contextErr
	}
	if err != nil {
		if errors.Is(err, errUpToDate) {
			if !item.ready(installCtx, filepath.Join(m.root, item.ID)) {
				return false, errors.New("installed tool cannot run for this project")
			}
			if err := installCtx.Err(); err != nil {
				return false, err
			}
			return false, m.refreshStatus(item.recipe)
		}
		return false, err
	}
	if !item.ready(installCtx, stage) {
		return false, errors.New("installer did not provide a complete, compatible tool")
	}
	if err := m.writeStatus(stage, version); err != nil {
		return false, err
	}
	if err := installCtx.Err(); err != nil {
		return false, err
	}
	return replaceDirectory(stage, filepath.Join(m.root, item.ID))
}

// errUpToDate lets an installer refresh its status without replacing files.
var errUpToDate = errors.New("managed tool is up to date")

func (m *Manager) refreshStatus(item recipe) error {
	root := filepath.Join(m.root, item.ID)
	_, version, err := readStatus(root)
	if err != nil {
		version = ""
	}
	return m.writeStatus(root, version)
}

// installedVersion returns the upstream version marker of a complete
// installation. Incomplete installations report no version so they are always
// reinstalled.
func (m *Manager) installedVersion(item recipe) string {
	root := filepath.Join(m.root, item.ID)
	if !installationReady(item, root) {
		return ""
	}
	_, version, err := readStatus(root)
	if err != nil {
		return ""
	}
	return version
}

// Disabled reports whether automatic installation is turned off. Existing
// managed tools continue to resolve; only installations and updates stop.
func Disabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WINGMAN_MANAGED_TOOLS"))) {
	case "0", "false", "off", "disable", "disabled":
		return true
	default:
		return false
	}
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
	for _, directory := range item.WorkingDirs {
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			return directory
		}
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
	case installerDotnet:
		// The runtime-locating launcher fronts the apphost under tools/;
		// installations from before that layout must be replaced.
		for _, command := range item.Commands {
			name := command
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			if !tooling.Executable(filepath.Join(root, "tools", name)) {
				return false
			}
		}
		return true
	case installerMaven:
		if item.ID == "java-debug" {
			return len(javaDebugBundlesAt(root)) > 0
		}
		return true
	default:
		return true
	}
}

func (m *Manager) installRecipe(ctx context.Context, item recipe, stage string) (string, error) {
	switch item.Kind {
	case installerGo:
		return m.installGo(ctx, item, stage)

	case installerRustup:
		return m.installRustup(ctx, item, stage)

	case installerNPM:
		return m.installNPM(ctx, item, stage)

	case installerPython:
		return m.installPython(ctx, item, stage)

	case installerDotnet:
		return m.installDotnet(ctx, item, stage)

	case installerMaven:
		return m.installMaven(ctx, item, stage)

	case installerGitHub:
		return m.installGitHub(ctx, item, stage)

	default:
		return "", fmt.Errorf("unsupported installer %q", item.Kind)
	}
}

func resolveInstalledCommand(root, command string) string {
	return tooling.Find([]string{
		filepath.Join(root, "bin"),
		filepath.Join(root, "Scripts"),
		filepath.Join(root, "node_modules", ".bin"),
	}, command)
}

// replaceDirectory reports whether stage became the active installation. An
// error after activation only means the previous directory could not yet be
// removed; recovery retries that cleanup on the next update.
func replaceDirectory(stage, target string) (bool, error) {
	pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-pending")
	if filepath.Clean(stage) != filepath.Clean(pending) {
		// A newer installation supersedes any earlier update waiting for
		// restart, including when the active directory can now be replaced.
		if err := os.RemoveAll(pending); err != nil {
			return false, err
		}
	}
	backup := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-old-"+uuid.NewString())
	hadTarget := false
	if _, err := os.Stat(target); err == nil {
		hadTarget = true
		if err := os.Rename(target, backup); err != nil {
			if filepath.Clean(stage) == filepath.Clean(pending) {
				return false, err
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
		if !installationReady(item, pending) {
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
	path := filepath.Join(root, lockName)
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
	process.Hide(command)
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

func quotePOSIXShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
