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

	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

const (
	statusName     = ".wingman"
	updateLockName = ".update-lock"
	updateInterval = 24 * time.Hour
	staleLockAge   = 30 * time.Minute
	lockHeartbeat  = time.Minute
)

type installerKind string

const (
	installerGo         installerKind = "go"
	installerCargo      installerKind = "cargo"
	installerNPM        installerKind = "npm"
	installerPython     installerKind = "python"
	installerDotnet     installerKind = "dotnet"
	installerJavaScript installerKind = "javascript"
	installerBrowser    installerKind = "browser"
	installerCodeLLDB   installerKind = "codelldb"
	installerNetCoreDbg installerKind = "netcoredbg"
	installerJava       installerKind = "java"
)

// Requirement lists equivalent commands in preference order. The first
// command Wingman knows how to manage selects the installation recipe.
type Requirement struct {
	Alternatives []string
}

// Progress identifies the managed tool currently being checked or installed.
type Progress struct {
	Tool    string
	Current int
	Total   int
}

type recipe struct {
	ID       string
	Kind     installerKind
	Packages []string
	Commands []string
}

// The catalog is deliberately small and deterministic. Ecosystem-specific
// recipes live beside their installers; SDKs and compilers remain external.
var catalog = allRecipes()

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
	mu        sync.Mutex
}

// New creates a manager rooted at WINGMAN_HOME/tools.
func New() (*Manager, error) {
	root, err := layout.WingmanPath("tools")
	if err != nil {
		return nil, err
	}
	manager := newManager(root)
	manager.install = manager.installRecipe
	return manager, nil
}

func newManager(root string) *Manager {
	manager := &Manager{
		root:      filepath.Clean(root),
		now:       time.Now,
		look:      exec.LookPath,
		run:       runCommand,
		fetch:     fetchURL,
		byCommand: make(map[string]recipe),
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
// installation is checked at most once per day; failed checks remain eligible
// for the next run. Successful updates replace the prior directory and remove
// it rather than accumulating versions.
func (m *Manager) Update(ctx context.Context, requirements []Requirement, progress ...func(Progress)) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	selected := make(map[string]recipe)
	for _, requirement := range requirements {
		for _, command := range requirement.Alternatives {
			if item, ok := m.byCommand[command]; ok {
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
				report(Progress{Tool: item.ID, Current: index + 1, Total: len(ids)})
			}
		}
		if err := recoverInterruptedUpdate(m.root, item.ID); err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("recover %s: %w", item.ID, err))
			continue
		}
		if m.fresh(item) {
			continue
		}
		updated, err := m.updateOne(ctx, item)
		changed = changed || updated
		if err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("update %s: %w", item.ID, err))
			continue
		}
	}
	return changed, errors.Join(updateErrors...)
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
	if err := m.install(ctx, item, stage); err != nil {
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

	case installerCargo:
		return m.installCargo(ctx, item, stage)

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
			if executableFile(candidate) {
				return candidate
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

// replaceDirectory reports whether stage became the active installation. An
// error after activation only means the previous directory could not yet be
// removed; recovery retries that cleanup on the next update.
func replaceDirectory(stage, target string) (bool, error) {
	backup := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-old-"+uuid.NewString())
	hadTarget := false
	if _, err := os.Stat(target); err == nil {
		hadTarget = true
		if err := os.Rename(target, backup); err != nil {
			return false, err
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

func recoverInterruptedUpdate(root, id string) error {
	target := filepath.Join(root, id)
	backups, err := filepath.Glob(filepath.Join(root, "."+id+"-old-*"))
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
	stages, err := filepath.Glob(filepath.Join(root, "."+id+"-install-*"))
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
	command.Env = env
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
