package devtools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

func TestNewUsesToolsDirectoryWithoutStoreSegment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WINGMAN_HOME", home)
	manager, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := manager.Root(), filepath.Join(home, "tools"); got != want {
		t.Fatalf("Root = %q, want %q", got, want)
	}
}

func TestUpdateSelectsPreferredManagedAlternativeAndRemovesOld(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	manager.now = func() time.Time { return time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC) }
	var installed []string
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		installed = append(installed, item.ID)
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return "", err
			}
		}
		return "", nil
	}

	old := filepath.Join(root, "typescript-language-server", "old.txt")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	var progress []Progress
	changed, err := manager.Update(context.Background(), []Requirement{
		{Alternatives: []string{"unknown", "tsc", "typescript-language-server"}},
		{Alternatives: []string{"typescript-language-server"}},
	}, func(update Progress) { progress = append(progress, update) })
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("Update did not report a change")
	}
	if len(installed) != 1 || installed[0] != "typescript-language-server" {
		t.Fatalf("installed = %v", installed)
	}
	if len(progress) != 2 || progress[0].Tool != "typescript-language-server" || progress[0].Label != "TypeScript language tools" {
		t.Fatalf("progress = %#v", progress)
	}
	if progress[0].Phase != ProgressChecking || progress[1].Phase != ProgressInstalling {
		t.Fatalf("progress phases = %#v", progress)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old installation remains: %v", err)
	}
	if got := manager.Resolve("typescript-language-server"); got == "" {
		t.Fatal("managed command was not resolved")
	}
	status, err := os.ReadFile(filepath.Join(root, "typescript-language-server", statusName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(status))); err != nil {
		t.Fatalf("managed status = %q: %v", status, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "-old-") || strings.Contains(entry.Name(), "-install-") {
			t.Fatalf("temporary installation remains: %s", entry.Name())
		}
	}
}

func TestUpdateSkipsFreshInstallation(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	installCount := 0
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		installCount++
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	requirements := []Requirement{{Alternatives: []string{"gopls"}}}
	var phases []ProgressPhase
	report := func(progress Progress) { phases = append(phases, progress.Phase) }

	if changed, err := manager.Update(context.Background(), requirements, report); err != nil || !changed {
		t.Fatalf("first Update = %v, %v", changed, err)
	}
	if want := []ProgressPhase{ProgressChecking, ProgressInstalling}; !reflect.DeepEqual(phases, want) {
		t.Fatalf("install phases = %v, want %v", phases, want)
	}
	phases = nil
	if changed, err := manager.Update(context.Background(), requirements, report); err != nil || changed {
		t.Fatalf("fresh Update = %v, %v", changed, err)
	}
	if want := []ProgressPhase{ProgressChecking}; !reflect.DeepEqual(phases, want) {
		t.Fatalf("fresh phases = %v, want %v", phases, want)
	}
	if installCount != 1 {
		t.Fatalf("install count = %d", installCount)
	}

	now = now.Add(updateInterval)
	phases = nil
	if changed, err := manager.Update(context.Background(), requirements, report); err != nil || !changed {
		t.Fatalf("stale Update = %v, %v", changed, err)
	}
	if want := []ProgressPhase{ProgressChecking, ProgressUpdating}; !reflect.DeepEqual(phases, want) {
		t.Fatalf("update phases = %v, want %v", phases, want)
	}
	if installCount != 2 {
		t.Fatalf("install count after refresh = %d", installCount)
	}
}

func TestUpdateKeepsCurrentInstallationWhenInstallerFails(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		return "", os.ErrPermission
	}
	current := filepath.Join(root, "gopls", "bin", tooling.Candidates(runtime.GOOS, "gopls")[0])
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}

	changed, err := manager.Update(context.Background(), []Requirement{{Alternatives: []string{"gopls"}}})
	if err == nil || changed {
		t.Fatalf("Update = %v, %v", changed, err)
	}
	if IsUnavailable(err) {
		t.Fatalf("refresh failure marked the existing tool unavailable: %v", err)
	}
	data, readErr := os.ReadFile(current)
	if readErr != nil || string(data) != "current" {
		t.Fatalf("current installation changed: %q, %v", data, readErr)
	}
}

func TestStatusChecksToolsWithoutInstallation(t *testing.T) {
	for _, source := range []string{"missing", "managed", "project", "system"} {
		t.Run(source, func(t *testing.T) {
			t.Setenv("WINGMAN_MANAGED_TOOLS", "on")
			root := filepath.Join(t.TempDir(), "tools")
			workspace := t.TempDir()
			manager := newManager(root)
			manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
			manager.install = func(context.Context, recipe, string) (string, error) {
				t.Fatal("status checking or reusing an installed debugger invoked the installer")
				return "", nil
			}
			switch source {
			case "managed":
				if err := writeTestCommand(filepath.Join(root, "delve"), "dlv"); err != nil {
					t.Fatal(err)
				}
			case "project":
				if err := writeTestCommand(filepath.Join(workspace, ".venv"), "dlv"); err != nil {
					t.Fatal(err)
				}
			case "system":
				system := t.TempDir()
				if err := writeTestCommand(system, "dlv"); err != nil {
					t.Fatal(err)
				}
				manager.look = func(string) (string, error) { return resolveInstalledCommand(system, "dlv"), nil }
			}
			requirements := []Requirement{{Alternatives: []string{"dlv"}, Workspace: workspace}}
			statuses, err := manager.Status(context.Background(), requirements)
			if err != nil || len(statuses) != 1 {
				t.Fatalf("Status = %+v, %v", statuses, err)
			}
			if status := statuses[0]; status.Tool != "delve" || status.Label != "Go debugger" || status.Installed != (source == "managed") || !status.Installable {
				t.Fatalf("tool status = %+v", status)
			}
			if source != "managed" {
				if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("status check changed tools directory: %v", err)
				}
				t.Setenv("WINGMAN_MANAGED_TOOLS", "off")
				statuses, err := manager.Status(context.Background(), requirements)
				if err != nil || statuses[0].Installable {
					t.Fatalf("disabled status = %+v, %v", statuses, err)
				}
			}
		})
	}
}

func TestMissingPrerequisiteMakesManagedToolNotApplicable(t *testing.T) {
	manager := newManager(t.TempDir())
	manager.prerequisite = manager.missingPrerequisite
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	manager.install = func(context.Context, recipe, string) (string, error) {
		t.Fatal("automatic update invoked an installer without its runtime")
		return "", nil
	}
	requirements := []Requirement{{Alternatives: []string{"gopls"}}}
	var phases []ProgressPhase
	changed, err := manager.Update(context.Background(), requirements, func(progress Progress) {
		phases = append(phases, progress.Phase)
	})
	if err != nil || changed || len(phases) != 0 {
		t.Fatalf("Update = %v, %v; phases = %v", changed, err, phases)
	}

	statuses, err := manager.Status(context.Background(), requirements)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("Status = %+v, %v", statuses, err)
	}
	status := statuses[0]
	if status.Installed || status.Installable || status.UnavailableReason != "Requires Go" {
		t.Fatalf("status = %+v", status)
	}
}

func TestDebuggerRuntimePrerequisitesAreReportedWithoutInstallation(t *testing.T) {
	for command, reason := range map[string]string{
		"dlv":                "Requires Go",
		"debugpy-adapter":    "Requires Python",
		"java-debug-adapter": "Requires Java",
		"js-debug-adapter":   "Requires Node.js",
		"netcoredbg":         "Requires the .NET SDK",
	} {
		t.Run(command, func(t *testing.T) {
			manager := newManager(t.TempDir())
			manager.prerequisite = manager.missingPrerequisite
			manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
			statuses, err := manager.Status(context.Background(), []Requirement{{Alternatives: []string{command}}})
			if err != nil || len(statuses) != 1 {
				t.Fatalf("Status = %+v, %v", statuses, err)
			}
			if statuses[0].Installable || statuses[0].UnavailableReason != reason {
				t.Fatalf("status = %+v, want reason %q", statuses[0], reason)
			}
		})
	}
}

func TestLLDBCanBeInstalledWithoutRust(t *testing.T) {
	t.Setenv("WINGMAN_MANAGED_TOOLS", "on")
	manager := newManager(t.TempDir())
	manager.prerequisite = manager.missingPrerequisite
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	statuses, err := manager.Status(context.Background(), []Requirement{{Alternatives: []string{"codelldb"}}})
	if err != nil || len(statuses) != 1 || !statuses[0].Installable || statuses[0].UnavailableReason != "" {
		t.Fatalf("LLDB requires a language runtime: %+v, %v", statuses, err)
	}
}

func TestMavenRecipeUsesWrapperAsInstallerPrerequisite(t *testing.T) {
	workspace := t.TempDir()
	manager := newManager(t.TempDir())
	manager.prerequisite = manager.missingPrerequisite
	manager.look = func(command string) (string, error) {
		if command == "java" {
			return "/runtime/java", nil
		}
		return "", exec.ErrNotFound
	}
	manager.install = func(context.Context, recipe, string) (string, error) {
		t.Fatal("automatic update invoked Maven without Maven or a wrapper")
		return "", nil
	}
	requirements := []Requirement{{Alternatives: []string{"jdtls"}, Workspace: workspace}}
	if changed, err := manager.Update(context.Background(), requirements); err != nil || changed {
		t.Fatalf("Update without Maven = %v, %v", changed, err)
	}
	statuses, err := manager.Status(context.Background(), requirements)
	if err != nil || len(statuses) != 1 || statuses[0].UnavailableReason != "Requires Maven or a Maven wrapper" {
		t.Fatalf("Status without Maven = %+v, %v", statuses, err)
	}

	wrapper := filepath.Join(workspace, "mvnw")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	statuses, err = manager.Status(context.Background(), requirements)
	if err != nil || len(statuses) != 1 || !statuses[0].Installable || statuses[0].UnavailableReason != "" {
		t.Fatalf("Status with wrapper = %+v, %v", statuses, err)
	}
}

func TestUpdateValidatesVersionBeforeReplacingInstalledTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test versions use POSIX scripts")
	}
	manager := newManager(t.TempDir())
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	item := manager.byCommand["tsc"]
	root := filepath.Join(manager.root, item.ID)
	writeVersion := func(root, version string) error {
		for _, command := range item.Commands {
			if err := writeTestCommand(root, command); err != nil {
				return err
			}
		}
		return os.WriteFile(filepath.Join(root, "bin", "tsc"), []byte("#!/bin/sh\nprintf 'Version "+version+"\\n'\n"), 0o755)
	}
	if err := writeVersion(root, "7.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := manager.writeStatus(root, "working-version"); err != nil {
		t.Fatal(err)
	}
	checkedAt := now
	now = now.Add(2 * updateInterval)
	manager.install = func(_ context.Context, _ recipe, stage string) (string, error) {
		return "incompatible-version", writeVersion(stage, "6.0.0")
	}
	requirements := []Requirement{{Alternatives: []string{"tsc"}, MinimumMajorVersions: map[string]int{"tsc": 7}}}
	if changed, err := manager.Update(context.Background(), requirements); changed || err == nil || IsUnavailable(err) {
		t.Fatalf("incompatible update = %v, %v", changed, err)
	}
	if !requirements[0].installed(context.Background(), manager.Resolve) {
		t.Fatal("working tool was replaced by incompatible update")
	}
	if stamp, version, err := readStatus(root); err != nil || !stamp.Equal(checkedAt) || version != "working-version" {
		t.Fatalf("failed update changed active status: %v, %q, %v", stamp, version, err)
	}
}

func TestCancelledInstallDoesNotActivateCompletedStage(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%v", existing), func(t *testing.T) {
			manager := newManager(t.TempDir())
			root := filepath.Join(manager.root, "gopls")
			if existing {
				if err := writeTestCommand(root, "gopls"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "original"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager.install = func(_ context.Context, _ recipe, stage string) (string, error) {
				if err := writeTestCommand(stage, "gopls"); err != nil {
					return "", err
				}
				cancel()
				return "new-version", nil
			}
			if changed, err := manager.Update(ctx, []Requirement{{Alternatives: []string{"gopls"}}}); changed || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled completed install = %v, %v", changed, err)
			}
			if existing {
				if _, err := os.Stat(filepath.Join(root, "original")); err != nil {
					t.Fatalf("original installation was replaced: %v", err)
				}
			} else if manager.Resolve("gopls") != "" {
				t.Fatal("cancelled installation was activated")
			}
			if _, err := os.Stat(manager.retryPath(manager.byCommand["gopls"])); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cancellation created retry backoff: %v", err)
			}
		})
	}
}

func TestUpdateOnDemandRechecksConsentAfterWaiting(t *testing.T) {
	manager := newManager(t.TempDir())
	if err := writeTestCommand(filepath.Join(manager.root, "delve"), "dlv"); err != nil {
		t.Fatal(err)
	}
	requirements := []Requirement{{Alternatives: []string{"dlv"}}}
	statuses, err := manager.Status(context.Background(), requirements)
	if err != nil || len(statuses) != 1 || !statuses[0].Installed {
		t.Fatalf("initial status = %+v, %v", statuses, err)
	}
	manager.install = func(context.Context, recipe, string) (string, error) {
		t.Error("installed missing debugger without confirmation")
		return "", errors.New("unexpected install")
	}
	manager.updates <- struct{}{}
	result := make(chan error, 1)
	go func() {
		_, err := manager.UpdateOnDemand(context.Background(), requirements, false)
		result <- err
	}()
	removeErr := os.Remove(manager.Resolve("dlv"))
	<-manager.updates
	if removeErr != nil {
		t.Fatal(removeErr)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrInstallRequired) {
			t.Fatalf("queued update = %v, want installation confirmation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued update did not finish")
	}
}

func TestUpdateOnDemandRefreshesManagedDebuggerAndRetriesFailures(t *testing.T) {
	manager := newManager(t.TempDir())
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	attempts := 0
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		attempts++
		if attempts == 1 {
			return "", errors.New("download failed")
		}
		return "", writeTestCommand(stage, "dlv")
	}
	requirements := []Requirement{{Alternatives: []string{"dlv"}}}
	if changed, err := manager.UpdateOnDemand(context.Background(), requirements, true); changed || !IsUnavailable(err) {
		t.Fatalf("failed install = %v, %v", changed, err)
	}
	if changed, err := manager.UpdateOnDemand(context.Background(), requirements, true); !changed || err != nil {
		t.Fatalf("immediate retry = %v, %v", changed, err)
	}
	if manager.Resolve("dlv") == "" {
		t.Fatal("installed debugger was not resolved")
	}
	if changed, err := manager.UpdateOnDemand(context.Background(), requirements, true); changed || err != nil {
		t.Fatalf("reuse fresh debugger = %v, %v", changed, err)
	}
	if attempts != 2 {
		t.Fatalf("install attempts = %d, want 2", attempts)
	}
	now = now.Add(2 * updateInterval)
	var phases []ProgressPhase
	if changed, err := manager.UpdateOnDemand(context.Background(), requirements, false, func(progress Progress) { phases = append(phases, progress.Phase) }); !changed || err != nil {
		t.Fatalf("refresh debugger = %v, %v", changed, err)
	}
	if attempts != 3 || !reflect.DeepEqual(phases, []ProgressPhase{ProgressChecking, ProgressUpdating}) {
		t.Fatalf("update attempts = %d, phases = %v", attempts, phases)
	}
	now = now.Add(updateInterval)
	manager.install = func(context.Context, recipe, string) (string, error) { return "", errors.New("update failed") }
	if changed, err := manager.UpdateOnDemand(context.Background(), requirements, true); changed || err == nil || IsUnavailable(err) {
		t.Fatalf("failed refresh = %v, %v", changed, err)
	}
	if manager.Resolve("dlv") == "" {
		t.Fatal("failed update removed the working managed debugger")
	}
}

func TestUpdateInstallsManagedToolEvenWhenEveryProjectHasAProjectTool(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	projects := []string{filepath.Join(workspace, "one"), filepath.Join(workspace, "two")}
	for _, project := range projects {
		command := filepath.Join(project, "node_modules", ".bin", tooling.Candidates(runtime.GOOS, "typescript-language-server")[0])
		if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(command, []byte("server"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manager := newManager(root)
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	installCount := 0
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		installCount++
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	changed, err := manager.Update(context.Background(), []Requirement{{
		Alternatives: []string{"typescript-language-server"}, Workspace: workspace, Projects: projects,
	}})
	if err != nil || !changed || installCount != 1 {
		t.Fatalf("Update = %v, %v; installs = %d", changed, err, installCount)
	}
}

func TestUpdateInstallsManagedToolForMultipleProjects(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	projects := []string{filepath.Join(workspace, "one"), filepath.Join(workspace, "two")}
	for _, project := range projects {
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	local := filepath.Join(projects[0], "node_modules", ".bin", tooling.Candidates(runtime.GOOS, "typescript-language-server")[0])
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("server"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := newManager(root)
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	changed, err := manager.Update(context.Background(), []Requirement{{
		Alternatives: []string{"typescript-language-server"}, Workspace: workspace, Projects: projects,
	}})
	if err != nil || !changed {
		t.Fatalf("Update = %v, %v", changed, err)
	}
}

func TestUpdateCarriesAllProjectDirectoriesIntoSharedRecipe(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	projects := []string{filepath.Join(workspace, "one"), filepath.Join(workspace, "two")}
	for _, project := range projects {
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manager := newManager(root)
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		if !reflect.DeepEqual(item.WorkingDirs, projects) {
			t.Fatalf("working directories = %v, want %v", item.WorkingDirs, projects)
		}
		return "", writeTestCommand(stage, "rust-analyzer")
	}
	changed, err := manager.Update(context.Background(), []Requirement{{
		Alternatives: []string{"rust-analyzer"}, Workspace: workspace, Projects: projects,
	}})
	if err != nil || !changed {
		t.Fatalf("Update = %v, %v", changed, err)
	}
}

func TestUpdateRejectsProjectCommandBelowMinimumVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	root := t.TempDir()
	workspace := t.TempDir()
	local := filepath.Join(workspace, "node_modules", ".bin", "tsc")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("#!/bin/sh\nprintf 'Version 6.0.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := newManager(root)
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	changed, err := manager.Update(context.Background(), []Requirement{{
		Alternatives: []string{"tsc", "typescript-language-server"}, Workspace: workspace, Projects: []string{workspace},
		MinimumMajorVersions: map[string]int{"tsc": 7},
	}})
	if err != nil || !changed {
		t.Fatalf("Update = %v, %v", changed, err)
	}
}

func TestManagedToolMustRunForEveryProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	workspace := t.TempDir()
	projects := []string{filepath.Join(workspace, "one"), filepath.Join(workspace, "two")}
	for _, project := range projects {
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projects[0], ".available"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	manager := newManager(t.TempDir())
	command := filepath.Join(manager.root, "rust-analyzer", "bin", "rust-analyzer")
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("#!/bin/sh\n[ -f .available ] || exit 1\nprintf 'rust-analyzer 1.0.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	requirement := Requirement{
		Alternatives: []string{"rust-analyzer"}, Workspace: workspace, Projects: projects,
		MinimumMajorVersions: map[string]int{"rust-analyzer": tooling.ProbeExecutes},
	}
	if requirement.installed(context.Background(), manager.Resolve) {
		t.Fatal("managed tool was accepted despite failing in one project")
	}
	if err := manager.writeStatus(filepath.Join(manager.root, "rust-analyzer"), "test-version"); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	manager.install = func(context.Context, recipe, string) (string, error) {
		attempts++
		if err := os.WriteFile(filepath.Join(projects[1], ".available"), nil, 0o644); err != nil {
			return "", err
		}
		return "", errUpToDate
	}
	if changed, err := manager.Update(context.Background(), []Requirement{requirement}); err != nil || !changed {
		t.Fatalf("repair freshly checked tool after project toolchain changed = %v, %v", changed, err)
	}
	if !requirement.installed(context.Background(), manager.Resolve) {
		t.Fatal("managed tool was rejected after succeeding in every project")
	}
	if changed, err := manager.Update(context.Background(), []Requirement{requirement}); err != nil || changed || attempts != 1 {
		t.Fatalf("reuse repaired tool = %v, %v, attempts %d", changed, err, attempts)
	}
}

func TestUpdateBacksOffFailedMissingInstall(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	attempts := 0
	manager.install = func(context.Context, recipe, string) (string, error) {
		attempts++
		return "", errors.New("registry blocked")
	}
	requirements := []Requirement{{Alternatives: []string{"gopls"}}}
	if _, err := manager.Update(context.Background(), requirements); !IsUnavailable(err) {
		t.Fatalf("first error = %v, want unavailable", err)
	}
	if _, err := manager.Update(context.Background(), requirements); !IsUnavailable(err) {
		t.Fatalf("backoff error = %v, want unavailable", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts during backoff = %d, want 1", attempts)
	}
	now = now.Add(retryInterval)
	if _, err := manager.Update(context.Background(), requirements); !IsUnavailable(err) {
		t.Fatalf("retry error = %v, want unavailable", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts after backoff = %d, want 2", attempts)
	}
}

func TestUpdateDoesNotBackOffCancellation(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	attempts := 0
	ctx, cancel := context.WithCancel(context.Background())
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		attempts++
		if attempts == 1 {
			cancel()
			return "", context.Canceled
		}
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	requirements := []Requirement{{Alternatives: []string{"gopls"}}}
	if _, err := manager.Update(ctx, requirements); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled update error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".gopls-retry")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancellation created retry backoff: %v", err)
	}
	if changed, err := manager.Update(context.Background(), requirements); err != nil || !changed {
		t.Fatalf("immediate retry = %v, %v", changed, err)
	}
	if attempts != 2 {
		t.Fatalf("install attempts = %d, want 2", attempts)
	}
}

func TestUpdateWaitingForAnotherUpdateHonorsContext(t *testing.T) {
	manager := newManager(t.TempDir())
	manager.updates <- struct{}{}
	defer func() { <-manager.updates }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Update(ctx, []Requirement{{Alternatives: []string{"gopls"}}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked update error = %v, want context cancellation", err)
	}
}

func TestUnavailableToolsCollectsJoinedErrors(t *testing.T) {
	err := errors.Join(
		fmt.Errorf("first: %w", &UnavailableError{Tool: "gopls", Err: errors.New("blocked")}),
		fmt.Errorf("second: %w", &UnavailableError{Tool: "debugpy", Err: errors.New("blocked")}),
		&UnavailableError{Tool: "gopls", Err: errors.New("duplicate")},
	)
	want := []string{"debugpy", "gopls"}
	if got := UnavailableTools(err); !reflect.DeepEqual(got, want) {
		t.Fatalf("UnavailableTools = %v, want %v", got, want)
	}
}

func TestNPMInstallUsesDetectedProjectForRegistryConfiguration(t *testing.T) {
	manager := newManager(t.TempDir())
	project := t.TempDir()
	item := manager.byCommand["typescript-language-server"]
	item.WorkingDirs = []string{project}
	manager.look = func(command string) (string, error) {
		if command != "npm" {
			t.Fatalf("look command = %q", command)
		}
		return "/toolchain/npm", nil
	}
	stage := t.TempDir()
	manager.run = func(_ context.Context, _ string, _ []string, dir string, _ []string) ([]byte, error) {
		if dir != project {
			t.Fatalf("npm working directory = %q, want %q", dir, project)
		}
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if _, err := manager.installRecipe(context.Background(), item, stage); err != nil {
		t.Fatal(err)
	}
}

func TestPythonCommandsRemainRunnableAfterActivation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses POSIX shell scripts")
	}
	root := t.TempDir()
	manager := newManager(root)
	manager.install = manager.installRecipe
	manager.look = func(string) (string, error) { return "/usr/bin/python3", nil }
	manager.run = func(_ context.Context, _ string, args []string, _ string, _ []string) ([]byte, error) {
		if len(args) >= 3 && args[0] == "-m" && args[1] == "venv" {
			stage := args[2]
			if err := os.MkdirAll(filepath.Join(stage, "bin"), 0o755); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(stage, "bin", "python"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755)
		}
		if len(args) >= 3 && args[0] == "-m" && args[1] == "pip" {
			return nil, os.WriteFile(filepath.Join(root, "ignored"), nil, 0o600)
		}
		if len(args) == 2 && args[0] == "-c" {
			return []byte(`{"debugpy-adapter":"debugpy.adapter:main"}`), nil
		}
		return nil, fmt.Errorf("unexpected Python command: %v", args)
	}

	changed, err := manager.Update(context.Background(), []Requirement{{Alternatives: []string{"debugpy-adapter"}}})
	if err != nil || !changed {
		t.Fatalf("Update = %v, %v", changed, err)
	}
	command := manager.Resolve("debugpy-adapter")
	if command == "" {
		t.Fatal("relocatable debugpy adapter was not resolved")
	}
	contents, err := os.ReadFile(command)
	if err != nil || strings.Contains(string(contents), "-install-") {
		t.Fatalf("launcher contains staging path: %q, %v", contents, err)
	}
	output, err := exec.Command(command, "--probe").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "--probe") {
		t.Fatalf("activated launcher = %q, %v", output, err)
	}
}

func TestResolveRejectsMissingShebangInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows launchers do not use shebangs")
	}
	manager := newManager(t.TempDir())
	path := filepath.Join(manager.root, "debugpy", "bin", "debugpy-adapter")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/missing/staging/bin/python\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := manager.Resolve("debugpy-adapter"); got != "" {
		t.Fatalf("broken managed launcher resolved as %q", got)
	}
}

func TestRecoverInterruptedUpdateRestoresPreviousInstallation(t *testing.T) {
	root := t.TempDir()
	backup := filepath.Join(root, ".gopls-old-backup")
	command := filepath.Join(backup, "bin", tooling.Candidates(runtime.GOOS, "gopls")[0])
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	staleStage := filepath.Join(root, ".gopls-install-stale")
	if err := os.MkdirAll(staleStage, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := recoverInterruptedUpdate(root, "gopls"); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(root, "gopls", "bin", tooling.Candidates(runtime.GOOS, "gopls")[0])
	data, err := os.ReadFile(restored)
	if err != nil || string(data) != "previous" {
		t.Fatalf("restored installation = %q, %v", data, err)
	}
	if _, err := os.Stat(staleStage); !os.IsNotExist(err) {
		t.Fatalf("stale stage remains: %v", err)
	}
}

func TestRecoverInterruptedUpdateHandlesGlobCharactersInRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tools[managed]")
	backup := filepath.Join(root, ".gopls-old-backup")
	if err := writeTestCommand(backup, "gopls"); err != nil {
		t.Fatal(err)
	}
	if err := recoverInterruptedUpdate(root, "gopls"); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(filepath.Join(root, "gopls"), "gopls"); got == "" {
		t.Fatal("backup below a root containing '[' was not restored")
	}
}

func TestActivatePendingUpdateBeforeToolsStart(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
	if err := writeTestCommand(filepath.Join(root, "gopls"), "gopls"); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(root, ".gopls-pending")
	if err := writeTestCommand(pending, "gopls"); err != nil {
		t.Fatal(err)
	}
	command := resolveInstalledCommand(pending, "gopls")
	if err := os.WriteFile(command, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager.activatePendingUpdates()
	active, err := os.ReadFile(resolveInstalledCommand(filepath.Join(root, "gopls"), "gopls"))
	if err != nil || string(active) != "new" {
		t.Fatalf("active pending update = %q, %v", active, err)
	}
	if _, err := os.Stat(pending); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending directory remains: %v", err)
	}
}

func TestSuccessfulUpdateSupersedesPendingUpdate(t *testing.T) {
	manager := newManager(t.TempDir())
	pending := filepath.Join(manager.root, ".gopls-pending")
	for _, root := range []string{filepath.Join(manager.root, "gopls"), pending} {
		if err := writeTestCommand(root, "gopls"); err != nil {
			t.Fatal(err)
		}
	}
	manager.install = func(_ context.Context, _ recipe, stage string) (string, error) {
		if err := writeTestCommand(stage, "gopls"); err != nil {
			return "", err
		}
		return "", os.WriteFile(resolveInstalledCommand(stage, "gopls"), []byte("latest"), 0o755)
	}
	changed, err := manager.Update(context.Background(), []Requirement{{Alternatives: []string{"gopls"}}})
	if err != nil || !changed {
		t.Fatalf("update = %t, %v", changed, err)
	}
	manager.activatePendingUpdates()
	active, err := os.ReadFile(manager.Resolve("gopls"))
	if err != nil || string(active) != "latest" {
		t.Fatalf("restart replaced the latest installation: %q, %v", active, err)
	}
	if _, err := os.Stat(pending); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("superseded pending update remains: %v", err)
	}
}

func TestIncompletePendingUpdatePreservesInstalledTool(t *testing.T) {
	manager := newManager(t.TempDir())
	if err := writeTestCommand(filepath.Join(manager.root, "gopls"), "gopls"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(manager.root, ".gopls-pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager.activatePendingUpdates()
	if manager.Resolve("gopls") == "" {
		t.Fatal("incomplete pending update replaced the installed tool")
	}
}

func TestRecoverInterruptedUpdateRestoresNewestBackup(t *testing.T) {
	root := t.TempDir()
	oldTime := time.Now().Add(-time.Hour)
	newTime := oldTime.Add(30 * time.Minute)
	for name, value := range map[string]struct {
		content string
		stamp   time.Time
	}{
		".gopls-old-z": {content: "previous", stamp: oldTime},
		".gopls-old-a": {content: "newest", stamp: newTime},
	} {
		backup := filepath.Join(root, name)
		command := filepath.Join(backup, "bin", tooling.Candidates(runtime.GOOS, "gopls")[0])
		if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(command, []byte(value.content), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(backup, value.stamp, value.stamp); err != nil {
			t.Fatal(err)
		}
	}

	if err := recoverInterruptedUpdate(root, "gopls"); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(root, "gopls", "bin", tooling.Candidates(runtime.GOOS, "gopls")[0])
	data, err := os.ReadFile(command)
	if err != nil || string(data) != "newest" {
		t.Fatalf("restored installation = %q, %v", data, err)
	}
}

func TestUpdateLockReleasePreservesReplacementOwner(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, lockName)
	release, err := acquireUpdateLock(context.Background(), root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	release()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement lock = %q, %v", data, err)
	}
}

func TestUpdateLockRecoversLegacyStaleDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, lockName)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-staleLockAge - time.Minute)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	release, err := acquireUpdateLock(context.Background(), root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock remains after release: %v", err)
	}
	matches, err := filepath.Glob(path + "-stale-*")
	if err != nil || len(matches) != 0 {
		t.Fatalf("stale locks = %v, %v", matches, err)
	}
}

func TestCatalogContainsCuratedManagedTools(t *testing.T) {
	manager := newManager(t.TempDir())
	for _, command := range []string{"gopls", "dlv", "rust-analyzer", "csharp-ls", "jdtls", "java-debug-adapter", "typescript-language-server", "ty", "clangd", "debugpy-adapter", "js-debug-adapter", "codelldb", "netcoredbg"} {
		if !manager.CanManage(command) {
			t.Errorf("CanManage(%q) = false", command)
		}
	}
	for _, command := range []string{"kotlin-lsp", "intelephense", "phpactor", "chrome-for-testing"} {
		if manager.CanManage(command) {
			t.Errorf("CanManage(%q) = true for externally supplied tool", command)
		}
	}
}

func TestCatalogHasUserFacingLabelsAndUniqueCommands(t *testing.T) {
	ids := make(map[string]bool)
	commands := make(map[string]string)
	for _, item := range catalog {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Label) == "" {
			t.Errorf("incomplete catalog recipe: %#v", item)
		}
		if ids[item.ID] {
			t.Errorf("duplicate tool ID %q", item.ID)
		}
		ids[item.ID] = true
		for _, command := range item.Commands {
			if owner := commands[command]; owner != "" {
				t.Errorf("command %q belongs to both %q and %q", command, owner, item.ID)
			}
			commands[command] = item.ID
		}
	}
	if got := ToolLabel("not-managed"); got != "not-managed" {
		t.Fatalf("unknown tool label = %q", got)
	}
}

func TestManagedPythonInstallLeavesProjectVirtualEnvironmentUntouched(t *testing.T) {
	workspace := t.TempDir()
	command := filepath.Join(workspace, ".venv", "bin", "debugpy-adapter")
	if runtime.GOOS == "windows" {
		command = filepath.Join(workspace, ".venv", "Scripts", "debugpy-adapter.exe")
	}
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("project adapter"), 0o755); err != nil {
		t.Fatal(err)
	}

	manager := newManager(t.TempDir())
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	manager.install = func(_ context.Context, _ recipe, stage string) (string, error) {
		return "", writeTestCommand(stage, "debugpy-adapter")
	}
	changed, err := manager.Update(context.Background(), []Requirement{{
		Alternatives: []string{"debugpy-adapter"}, Workspace: workspace, Projects: []string{workspace},
	}})
	if err != nil || !changed {
		t.Fatalf("Update = %v, %v", changed, err)
	}
	if contents, err := os.ReadFile(command); err != nil || string(contents) != "project adapter" {
		t.Fatalf("project .venv command changed: %q, %v", contents, err)
	}
}

func TestSystemToolDoesNotSuppressManagedPackage(t *testing.T) {
	workspace := t.TempDir()
	manager := newManager(t.TempDir())
	manager.look = func(command string) (string, error) {
		return filepath.Join(string(filepath.Separator), "system", command), nil
	}
	manager.install = func(_ context.Context, item recipe, stage string) (string, error) {
		for _, command := range item.Commands {
			if err := writeTestCommand(stage, command); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	changed, err := manager.Update(context.Background(), []Requirement{{
		Alternatives: []string{"gopls"}, Workspace: workspace, Projects: []string{workspace},
	}})
	if err != nil || !changed {
		t.Fatalf("Update = %v, %v", changed, err)
	}
}

func TestSystemToolDoesNotHideUnavailableManagedInstall(t *testing.T) {
	workspace := t.TempDir()
	manager := newManager(t.TempDir())
	manager.look = func(command string) (string, error) {
		return filepath.Join(string(filepath.Separator), "system", command), nil
	}
	manager.install = func(context.Context, recipe, string) (string, error) {
		return "", errors.New("package registry is blocked")
	}
	changed, err := manager.Update(context.Background(), []Requirement{{
		Alternatives: []string{"gopls"}, Workspace: workspace, Projects: []string{workspace},
	}})
	if changed || err == nil {
		t.Fatalf("Update = %v, %v", changed, err)
	}
	if !IsUnavailable(err) {
		t.Fatalf("missing managed tool was not marked unavailable: %v", err)
	}
}

func TestRustAnalyzerUsesEveryProjectToolchain(t *testing.T) {
	firstProject := t.TempDir()
	secondProject := t.TempDir()
	manager := newManager(t.TempDir())
	manager.look = func(command string) (string, error) {
		if command != "rustup" {
			t.Fatalf("look command = %q", command)
		}
		return "/toolchain/rustup", nil
	}
	var calls []string
	manager.run = func(_ context.Context, command string, args []string, dir string, _ []string) ([]byte, error) {
		calls = append(calls, dir+" "+command+" "+strings.Join(args, " "))
		if reflect.DeepEqual(args, []string{"show", "active-toolchain"}) {
			if dir == firstProject {
				return []byte("nightly-aarch64-apple-darwin (overridden by rust-toolchain.toml)\n"), nil
			}
			if dir == secondProject {
				return []byte("stable-aarch64-apple-darwin (overridden by rust-toolchain.toml)\n"), nil
			}
		}
		if reflect.DeepEqual(args, []string{"run", "nightly-aarch64-apple-darwin", "rust-analyzer", "--version"}) {
			return []byte("rust-analyzer 1.2.3\n"), nil
		}
		if reflect.DeepEqual(args, []string{"run", "stable-aarch64-apple-darwin", "rust-analyzer", "--version"}) {
			return []byte("rust-analyzer 1.2.4\n"), nil
		}
		return nil, nil
	}
	item := manager.byCommand["rust-analyzer"]
	item.WorkingDirs = []string{firstProject, secondProject}
	stage := t.TempDir()
	version, err := manager.installRecipe(context.Background(), item, stage)
	if err != nil {
		t.Fatal(err)
	}
	wantVersion := "/toolchain/rustup|nightly-aarch64-apple-darwin=rust-analyzer 1.2.3|stable-aarch64-apple-darwin=rust-analyzer 1.2.4"
	if version != wantVersion {
		t.Fatalf("version = %q", version)
	}
	want := []string{
		firstProject + " /toolchain/rustup show active-toolchain",
		secondProject + " /toolchain/rustup show active-toolchain",
		firstProject + " /toolchain/rustup component add --toolchain nightly-aarch64-apple-darwin rust-analyzer",
		firstProject + " /toolchain/rustup run nightly-aarch64-apple-darwin rust-analyzer --version",
		secondProject + " /toolchain/rustup component add --toolchain stable-aarch64-apple-darwin rust-analyzer",
		secondProject + " /toolchain/rustup run stable-aarch64-apple-darwin rust-analyzer --version",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if resolveInstalledCommand(stage, "rust-analyzer") == "" {
		t.Fatal("rustup proxy launcher was not installed")
	}
}

func TestActiveRustToolchainIgnoresRustupSetupNoise(t *testing.T) {
	output := []byte("info: syncing channel updates for 'stable-aarch64-apple-darwin'\n" +
		"info: default toolchain set to 'stable-aarch64-apple-darwin'\n" +
		"stable-aarch64-apple-darwin (default)\n")
	if got := activeRustToolchain(output); got != "stable-aarch64-apple-darwin" {
		t.Fatalf("active toolchain = %q", got)
	}
	if got := activeRustToolchain([]byte("info: no active toolchain\n")); got != "" {
		t.Fatalf("informational output produced toolchain %q", got)
	}
}

func TestJDTLSUsesMavenProductArtifactAndProjectWrapper(t *testing.T) {
	const resolvedVersion = "1.61.0.202608210947"
	firstProject := t.TempDir()
	wrapperProject := t.TempDir()
	wrapperName := "mvnw"
	if runtime.GOOS == "windows" {
		wrapperName = "mvnw.cmd"
	}
	wrapper := filepath.Join(wrapperProject, wrapperName)
	if err := os.WriteFile(wrapper, []byte("wrapper"), 0o755); err != nil {
		t.Fatal(err)
	}

	manager := newManager(t.TempDir())
	manager.look = func(command string) (string, error) {
		t.Fatalf("looked for %q despite project Maven wrapper", command)
		return "", exec.ErrNotFound
	}
	item := manager.byCommand["jdtls"]
	item.WorkingDirs = []string{firstProject, wrapperProject}
	stage := t.TempDir()
	var gotCommand, gotDir string
	var gotArgs []string
	var gotPOM []byte
	manager.run = func(_ context.Context, command string, args []string, dir string, _ []string) ([]byte, error) {
		gotCommand, gotDir = command, dir
		gotArgs = append([]string(nil), args...)
		if len(args) >= 2 && args[0] == "--file" {
			gotPOM, _ = os.ReadFile(args[1])
		}
		return nil, writeTestJDTLS(stage, resolvedVersion)
	}
	version, err := manager.installRecipe(context.Background(), item, stage)
	if err != nil {
		t.Fatal(err)
	}
	if version != resolvedVersion || gotCommand != wrapper || gotDir != wrapperProject {
		t.Fatalf("install = version %q, command %q, dir %q", version, gotCommand, gotDir)
	}
	joined := strings.Join(gotArgs, "\n")
	for _, value := range []string{
		"--update-snapshots",
		"maven-dependency-plugin:" + mavenDependencyPluginVersion + ":unpack-dependencies",
		"-DincludeGroupIds=org.eclipse.jdt.ls",
		"-DincludeArtifactIds=org.eclipse.jdt.ls.product",
		"-DoutputDirectory=" + stage,
		"-DmarkersDirectory=" + filepath.Join(stage, ".maven-markers"),
	} {
		if !strings.Contains(joined, value) {
			t.Errorf("Maven arguments do not contain %q: %v", value, gotArgs)
		}
	}
	pomPath := filepath.Join(stage, ".wingman-jdtls-pom.xml")
	if len(gotArgs) < 2 || gotArgs[0] != "--file" || gotArgs[1] != pomPath {
		t.Fatalf("Maven project arguments = %v", gotArgs[:min(2, len(gotArgs))])
	}
	for _, value := range []string{jdtlsMavenRepository, "<version>" + mavenLatestVersion + "</version>", "<type>tar.gz</type>"} {
		if !bytes.Contains(gotPOM, []byte(value)) {
			t.Errorf("generated Maven project does not contain %q:\n%s", value, gotPOM)
		}
	}
}

func TestJDTLSKeepsInstallationWhenLatestReleaseIsUnchanged(t *testing.T) {
	const resolvedVersion = "1.61.0.202608210947"
	root := t.TempDir()
	manager := newManager(root)
	item := manager.byCommand["jdtls"]
	installed := filepath.Join(root, item.ID)
	if err := writeTestJDTLS(installed, resolvedVersion); err != nil {
		t.Fatal(err)
	}
	if err := manager.writeStatus(installed, resolvedVersion); err != nil {
		t.Fatal(err)
	}
	manager.look = func(command string) (string, error) {
		if command != "mvn" {
			t.Fatalf("look command = %q", command)
		}
		return "/toolchain/mvn", nil
	}
	stage := t.TempDir()
	manager.run = func(_ context.Context, _ string, _ []string, _ string, _ []string) ([]byte, error) {
		return nil, writeTestJDTLS(stage, resolvedVersion)
	}
	if _, err := manager.installRecipe(context.Background(), item, stage); !errors.Is(err, errUpToDate) {
		t.Fatalf("install error = %v, want errUpToDate", err)
	}
}

func TestJavaDebuggerUsesLatestMavenPluginAndProjectWrapper(t *testing.T) {
	const resolvedVersion = "0.53.1"
	project := t.TempDir()
	wrapperName := "mvnw"
	if runtime.GOOS == "windows" {
		wrapperName = "mvnw.cmd"
	}
	wrapper := filepath.Join(project, wrapperName)
	if err := os.WriteFile(wrapper, []byte("wrapper"), 0o755); err != nil {
		t.Fatal(err)
	}

	manager := newManager(t.TempDir())
	manager.look = func(command string) (string, error) {
		t.Fatalf("looked for %q despite project Maven wrapper", command)
		return "", exec.ErrNotFound
	}
	item := manager.byCommand["java-debug-adapter"]
	item.WorkingDirs = []string{project}
	stage := t.TempDir()
	var gotCommand, gotDir string
	var gotArgs []string
	manager.run = func(_ context.Context, command string, args []string, dir string, _ []string) ([]byte, error) {
		gotCommand, gotDir = command, dir
		gotArgs = append([]string(nil), args...)
		return nil, writeTestJavaDebugBundle(stage, resolvedVersion)
	}
	version, err := manager.installRecipe(context.Background(), item, stage)
	if err != nil {
		t.Fatal(err)
	}
	if version != resolvedVersion || gotCommand != wrapper || gotDir != project {
		t.Fatalf("install = version %q, command %q, dir %q", version, gotCommand, gotDir)
	}
	joined := strings.Join(gotArgs, "\n")
	for _, value := range []string{
		"--update-snapshots",
		"maven-dependency-plugin:" + mavenDependencyPluginVersion + ":copy",
		"-Dartifact=com.microsoft.java:com.microsoft.java.debug.plugin:" + mavenLatestVersion,
		"-DoutputDirectory=" + filepath.Join(stage, "java-debug"),
	} {
		if !strings.Contains(joined, value) {
			t.Errorf("Maven arguments do not contain %q: %v", value, gotArgs)
		}
	}
	if resolveInstalledCommand(stage, "java-debug-adapter") == "" {
		t.Fatal("Java debug availability marker was not installed")
	}
	if bundles := javaDebugBundlesAt(stage); len(bundles) != 1 {
		t.Fatalf("Java debug bundles = %v", bundles)
	}
}

func TestCSharpLanguageServerUsesDotnetTool(t *testing.T) {
	manager := newManager(t.TempDir())
	item := manager.byCommand["csharp-ls"]
	manager.look = func(command string) (string, error) {
		if command != "dotnet" {
			t.Fatalf("look command = %q", command)
		}
		return "/sdk/dotnet", nil
	}
	var gotArgs []string
	manager.run = func(_ context.Context, _ string, args []string, dir string, _ []string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return nil, writeTestCommand(dir, "csharp-ls")
	}
	stage := t.TempDir()
	if _, err := manager.installRecipe(context.Background(), item, stage); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"tool", "install", "--tool-path", filepath.Join(stage, "tools"), "--allow-roll-forward", "csharp-ls"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
	launcher := resolveInstalledCommand(stage, "csharp-ls")
	if launcher == "" {
		t.Fatal("csharp-ls launcher was not installed")
	}
	if runtime.GOOS != "windows" {
		contents, err := os.ReadFile(launcher)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "DOTNET_ROOT") || !strings.Contains(string(contents), "../tools/csharp-ls") || !strings.Contains(string(contents), "DOTNET_CMD='/sdk/dotnet'") {
			t.Fatalf("launcher does not target the tool through DOTNET_ROOT:\n%s", contents)
		}
	}
}

func writeTestCommand(root, command string) error {
	directory := filepath.Join(root, "bin")
	name := command
	if runtime.GOOS == "windows" {
		directory = filepath.Join(root, "Scripts")
		name += ".exe"
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, name), []byte("test"), 0o755)
}

func writeTestJDTLS(root, version string) error {
	if err := writeTestCommand(root, "jdtls"); err != nil {
		return err
	}
	plugins := filepath.Join(root, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(plugins, jdtlsCoreBundlePrefix+version+".jar"), []byte("test"), 0o644)
}

func writeTestJavaDebugBundle(root, version string) error {
	directory := filepath.Join(root, "java-debug")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, javaDebugBundlePrefix+version+".jar"), []byte("test"), 0o644)
}

func TestUpdateDisabledByEnvironment(t *testing.T) {
	t.Setenv("WINGMAN_MANAGED_TOOLS", "off")
	root := filepath.Join(t.TempDir(), "tools")
	manager := newManager(root)
	manager.install = func(context.Context, recipe, string) (string, error) {
		t.Fatal("installer ran while managed tools are disabled")
		return "", nil
	}
	changed, err := manager.Update(context.Background(), []Requirement{{Alternatives: []string{"gopls"}}})
	if err != nil || changed {
		t.Fatalf("disabled update = %v, %v", changed, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disabled update created the tools directory")
	}
}
