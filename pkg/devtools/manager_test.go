package devtools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	if len(progress) != 1 || progress[0].Tool != "typescript-language-server" || progress[0].Label != "TypeScript language tools" {
		t.Fatalf("progress = %#v", progress)
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

	if changed, err := manager.Update(context.Background(), requirements); err != nil || !changed {
		t.Fatalf("first Update = %v, %v", changed, err)
	}
	if changed, err := manager.Update(context.Background(), requirements); err != nil || changed {
		t.Fatalf("fresh Update = %v, %v", changed, err)
	}
	if installCount != 1 {
		t.Fatalf("install count = %d", installCount)
	}

	now = now.Add(updateInterval)
	if changed, err := manager.Update(context.Background(), requirements); err != nil || !changed {
		t.Fatalf("stale Update = %v, %v", changed, err)
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

func TestUpdateSkipsManagedInstallWhenEveryProjectHasAnExternalTool(t *testing.T) {
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
	manager.install = func(context.Context, recipe, string) (string, error) {
		installCount++
		return "", nil
	}
	changed, err := manager.Update(context.Background(), []Requirement{{
		Alternatives: []string{"typescript-language-server"}, Workspace: workspace, Projects: projects,
	}})
	if err != nil || changed || installCount != 0 {
		t.Fatalf("Update = %v, %v; installs = %d", changed, err, installCount)
	}
}

func TestUpdateInstallsFallbackWhenOneProjectLacksExternalTool(t *testing.T) {
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

func TestUpdateRejectsExternalCommandBelowMinimumVersion(t *testing.T) {
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

func TestUpdateBacksOffFailedMissingInstall(t *testing.T) {
	root := t.TempDir()
	manager := newManager(root)
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
	item.WorkingDir = project
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

func TestCleanMachineCatalogIncludesRustDotnetAndJava(t *testing.T) {
	manager := newManager(t.TempDir())
	for _, command := range []string{"rust-analyzer", "codelldb", "csharp-ls", "netcoredbg", "jdtls", "js-debug-adapter", "chrome-for-testing"} {
		if !manager.CanManage(command) {
			t.Errorf("CanManage(%q) = false", command)
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

func TestJavaScriptAdapterUsesVerifiedStandaloneRelease(t *testing.T) {
	archive := testTarGzip(t, map[string]string{
		"js-debug/src/dapDebugServer.js": "console.log('adapter')\n",
		"js-debug/LICENSE":               "MIT\n",
	})
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	metadata := fmt.Sprintf(`{"assets":[{"name":"js-debug-dap-v1.2.3.tar.gz","browser_download_url":"https://github.com/microsoft/vscode-js-debug/releases/download/v1.2.3/js-debug-dap-v1.2.3.tar.gz","digest":%q}]}`, digest)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		if address == javascriptReleaseURL {
			return []byte(metadata), nil
		}
		return archive, nil
	}
	stage := t.TempDir()
	if _, err := manager.installRecipe(context.Background(), manager.byCommand["js-debug-adapter"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "js-debug-adapter"); got == "" {
		t.Fatal("standalone JavaScript adapter launcher was not installed")
	}
	if _, err := os.Stat(filepath.Join(stage, "js-debug", "src", "dapDebugServer.js")); err != nil {
		t.Fatal(err)
	}
}

func TestJavaScriptAdapterRejectsArchiveTraversal(t *testing.T) {
	archive := testTarGzip(t, map[string]string{"../escape": "bad"})
	if err := extractTarGzip(archive, t.TempDir()); err == nil {
		t.Fatal("archive traversal was accepted")
	}
}

func TestChromeForTestingUsesPreferredPlatformArchive(t *testing.T) {
	platform, err := chromeForTestingPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	relativeExecutable, err := chromeForTestingExecutable("", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	relativeExecutable = strings.TrimPrefix(filepath.Clean(relativeExecutable), string(filepath.Separator))
	archive := testZip(t, map[string]testZipEntry{
		filepath.ToSlash(relativeExecutable): {contents: "browser", mode: 0o755},
	})
	downloadURL := "https://storage.example.test/chrome.zip"
	metadata := fmt.Sprintf(`{"channels":{"Stable":{"version":"123.0.0.1","downloads":{"chrome":[{"platform":%q,"url":%q}]}}}}`, platform, downloadURL)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		switch address {
		case chromeForTestingReleaseURL:
			return []byte(metadata), nil
		case downloadURL:
			return archive, nil
		default:
			return nil, fmt.Errorf("unexpected URL %s", address)
		}
	}
	stage := t.TempDir()
	if _, err := manager.installRecipe(context.Background(), manager.byCommand["chrome-for-testing"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "chrome-for-testing"); got == "" {
		t.Fatal("Chrome for Testing launcher was not installed")
	}
	if executable, err := chromeForTestingExecutable(stage, runtime.GOOS, runtime.GOARCH); err != nil || !tooling.Executable(executable) {
		t.Fatalf("Chrome executable = %q, %v", executable, err)
	}
}

func TestChromeForTestingFallsBackWhenStableLacksPlatform(t *testing.T) {
	metadata := `{"channels":{
		"Stable":{"version":"152","downloads":{"chrome":[{"platform":"linux64","url":"https://example.test/stable"}]}},
		"Beta":{"version":"153","downloads":{"chrome":[{"platform":"linux-arm64","url":"https://example.test/beta"}]}}
	}}`
	var release chromeForTestingRelease
	if err := json.Unmarshal([]byte(metadata), &release); err != nil {
		t.Fatal(err)
	}
	version, address := chromeForTestingDownload(release, "linux-arm64")
	if version != "153" || address != "https://example.test/beta" {
		t.Fatalf("download = %q, %q", version, address)
	}
}

func TestZipExtractorAllowsSafeRelativeSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require Windows developer mode")
	}
	archive := testZip(t, map[string]testZipEntry{
		"app/Versions/1/app":   {contents: "browser", mode: 0o755},
		"app/Versions/Current": {contents: "1", mode: os.ModeSymlink | 0o777},
	})
	root := t.TempDir()
	if err := extractZip(archive, root); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(root, "app", "Versions", "Current"))
	if err != nil || target != "1" {
		t.Fatalf("symlink = %q, %v", target, err)
	}
}

func TestCodeLLDBUsesVerifiedPlatformRelease(t *testing.T) {
	adapterName := "codelldb"
	if runtime.GOOS == "windows" {
		adapterName += ".exe"
	}
	archive := testZip(t, map[string]testZipEntry{
		"extension/adapter/" + adapterName: {contents: "adapter", mode: 0o755},
		"extension/lldb/runtime":           {contents: "lldb", mode: 0o644},
	})
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	assetName, err := codeLLDBAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	metadata := fmt.Sprintf(`{"assets":[{"name":%q,"browser_download_url":"https://example.test/codelldb.vsix","digest":%q}]}`, assetName, digest)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		if address == codeLLDBReleaseURL {
			return []byte(metadata), nil
		}
		return archive, nil
	}
	stage := t.TempDir()
	if _, err := manager.installRecipe(context.Background(), manager.byCommand["codelldb"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "codelldb"); got == "" {
		t.Fatal("CodeLLDB launcher was not installed")
	}
	if _, err := os.Stat(filepath.Join(stage, "extension", "lldb", "runtime")); err != nil {
		t.Fatal(err)
	}
}

func TestNetCoreDbgUsesVerifiedPlatformRelease(t *testing.T) {
	adapterName := "netcoredbg"
	if runtime.GOOS == "windows" {
		adapterName += ".exe"
	}
	archive := testZip(t, map[string]testZipEntry{
		"netcoredbg/" + adapterName: {contents: "adapter", mode: 0o755},
		"netcoredbg/runtime.dll":    {contents: "runtime", mode: 0o644},
	})
	assetName, err := netCoreDbgAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	if strings.HasSuffix(assetName, ".tar.gz") {
		archive = testTarGzip(t, map[string]string{
			"netcoredbg/" + adapterName: "adapter",
			"netcoredbg/runtime.dll":    "runtime",
		})
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	metadata := fmt.Sprintf(`{"assets":[{"name":%q,"browser_download_url":"https://example.test/netcoredbg","digest":%q}]}`, assetName, digest)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		if address == netCoreDbgReleaseURL {
			return []byte(metadata), nil
		}
		return archive, nil
	}
	stage := t.TempDir()
	if _, err := manager.installRecipe(context.Background(), manager.byCommand["netcoredbg"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "netcoredbg"); got == "" {
		t.Fatal("NetCoreDbg launcher was not installed")
	}
}

func TestJavaInstallsLatestVerifiedJDTLSAndDebugBundle(t *testing.T) {
	jdtlsArchive := testTarGzip(t, map[string]string{
		"bin/jdtls":                "#!/bin/sh\n",
		"plugins/jdtls-plugin.jar": "jdtls",
	})
	javaDebugArchive := testZip(t, map[string]testZipEntry{
		"extension/server/com.microsoft.java.debug.plugin-0.53.2.jar": {contents: "java-debug", mode: 0o644},
	})
	jdtlsFilename := "jdt-language-server-1.60.0-build.tar.gz"
	jdtlsBase := jdtlsMilestonesURL + "1.60.0/"
	javaDebugURL := "https://example.test/java-debug.vsix"
	javaDebugSHAURL := "https://example.test/java-debug.sha256"
	javaMetadata := fmt.Sprintf(`{"verified":true,"files":{"download":%q,"sha256":%q}}`, javaDebugURL, javaDebugSHAURL)

	manager := newManager(t.TempDir())
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		switch address {
		case jdtlsMilestonesURL:
			return []byte(`<a href='/jdtls/milestones/1.9.0'>old</a><a href='/jdtls/milestones/1.60.0'>latest</a>`), nil
		case jdtlsBase + "latest.txt":
			return []byte(jdtlsFilename), nil
		case jdtlsBase + jdtlsFilename:
			return jdtlsArchive, nil
		case jdtlsBase + jdtlsFilename + ".sha256":
			return []byte(fmt.Sprintf("%x", sha256.Sum256(jdtlsArchive))), nil
		case javaDebugLatestURL:
			return []byte(javaMetadata), nil
		case javaDebugURL:
			return javaDebugArchive, nil
		case javaDebugSHAURL:
			return []byte(fmt.Sprintf("%x", sha256.Sum256(javaDebugArchive))), nil
		default:
			return nil, fmt.Errorf("unexpected URL %s", address)
		}
	}
	stage := t.TempDir()
	if _, err := manager.installRecipe(context.Background(), manager.byCommand["jdtls"], stage); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstalledCommand(stage, "jdtls"); got == "" {
		t.Fatal("JDT LS launcher was not installed")
	}
	if bundles := javaDebugBundlesAt(stage); len(bundles) != 1 {
		t.Fatalf("java-debug bundles = %#v", bundles)
	}
}

func TestZipExtractorRejectsArchiveTraversal(t *testing.T) {
	archive := testZip(t, map[string]testZipEntry{"../escape": {contents: "bad", mode: 0o644}})
	if err := extractZip(archive, t.TempDir()); err == nil {
		t.Fatal("archive traversal was accepted")
	}
}

func TestRustAnalyzerUsesVerifiedOfficialRelease(t *testing.T) {
	manager := newManager(t.TempDir())
	item := manager.byCommand["rust-analyzer"]
	manager.look = func(string) (string, error) { return "", exec.ErrNotFound }
	assetName, err := rustAnalyzerAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	var archive bytes.Buffer
	if runtime.GOOS == "windows" {
		archive.Write(testZip(t, map[string]testZipEntry{
			"rust-analyzer.exe": {contents: "server", mode: 0o755},
		}))
	} else {
		writer := gzip.NewWriter(&archive)
		if _, err := writer.Write([]byte("server")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive.Bytes()))
	metadata := fmt.Sprintf(`{"assets":[{"name":%q,"browser_download_url":"https://example.test/rust-analyzer","digest":%q}]}`, assetName, digest)
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		if address == rustAnalyzerReleaseURL {
			return []byte(metadata), nil
		}
		return archive.Bytes(), nil
	}
	stage := t.TempDir()
	version, err := manager.installRecipe(context.Background(), item, stage)
	if err != nil {
		t.Fatal(err)
	}
	if version != "https://example.test/rust-analyzer" {
		t.Fatalf("version marker = %q", version)
	}
	if got := resolveInstalledCommand(stage, "rust-analyzer"); got == "" {
		t.Fatal("rust-analyzer was not installed")
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

func testTarGzip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	gzipWriter := gzip.NewWriter(&data)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, contents := range files {
		mode := int64(0o644)
		if strings.HasPrefix(name, "bin/") || strings.HasSuffix(name, "/netcoredbg") || strings.HasSuffix(name, "/netcoredbg.exe") {
			mode = 0o755
		}
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(contents))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type testZipEntry struct {
	contents string
	mode     os.FileMode
}

func testZip(t *testing.T, files map[string]testZipEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, entry := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestUpdateSkipsUnchangedGitHubRelease(t *testing.T) {
	archive := testTarGzip(t, map[string]string{
		"js-debug/src/dapDebugServer.js": "console.log('adapter')\n",
	})
	assetURL := "https://github.example.test/js-debug-dap-v1.2.3.tar.gz"
	metadata := fmt.Sprintf(`{"assets":[{"name":"js-debug-dap-v1.2.3.tar.gz","browser_download_url":%q}]}`, assetURL)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	downloads := 0
	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	manager.now = func() time.Time { return now }
	manager.look = func(string) (string, error) { return filepath.Join(t.TempDir(), "node"), nil }
	manager.fetch = func(_ context.Context, address string) ([]byte, error) {
		switch address {
		case javascriptReleaseURL:
			return []byte(metadata), nil
		case assetURL:
			downloads++
			return archive, nil
		default:
			return nil, fmt.Errorf("unexpected URL %s", address)
		}
	}

	requirements := []Requirement{{Alternatives: []string{"js-debug-adapter"}}}
	if changed, err := manager.Update(context.Background(), requirements); err != nil || !changed {
		t.Fatalf("first update = %v, %v", changed, err)
	}
	if downloads != 1 {
		t.Fatalf("downloads after install = %d", downloads)
	}

	now = now.Add(25 * time.Hour)
	if changed, err := manager.Update(context.Background(), requirements); err != nil || changed {
		t.Fatalf("stale update = %v, %v", changed, err)
	}
	if downloads != 1 {
		t.Fatalf("unchanged release was re-downloaded (%d downloads)", downloads)
	}
	if !manager.fresh(manager.byCommand["js-debug-adapter"]) {
		t.Fatal("skipped update did not refresh the status stamp")
	}

	assetURL2 := "https://github.example.test/js-debug-dap-v1.2.4.tar.gz"
	metadata = fmt.Sprintf(`{"assets":[{"name":"js-debug-dap-v1.2.4.tar.gz","browser_download_url":%q}]}`, assetURL2)
	previous := manager.fetch
	manager.fetch = func(ctx context.Context, address string) ([]byte, error) {
		if address == assetURL2 {
			downloads++
			return archive, nil
		}
		return previous(ctx, address)
	}
	now = now.Add(25 * time.Hour)
	if changed, err := manager.Update(context.Background(), requirements); err != nil || !changed {
		t.Fatalf("new release update = %v, %v", changed, err)
	}
	if downloads != 2 {
		t.Fatalf("new release was not downloaded (%d downloads)", downloads)
	}
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

func TestVerifySHA256AllowsOnlyAbsentPublishedChecksum(t *testing.T) {
	if err := verifySHA256([]byte("data"), ""); err != nil {
		t.Fatalf("missing checksum was rejected: %v", err)
	}
	if err := verifySHA256([]byte("data"), "sha256:"); err == nil {
		t.Fatal("malformed empty checksum was accepted")
	}
	if err := verifySHA256([]byte("data"), "sha256:0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("wrong checksum was accepted")
	}
}

func TestTarGzipExtractorHandlesPaxHeaderAndSymlink(t *testing.T) {
	var data bytes.Buffer
	compressor := gzip.NewWriter(&data)
	writer := tar.NewWriter(compressor)
	if err := writer.WriteHeader(&tar.Header{
		Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader,
		PAXRecords: map[string]string{"comment": "git archive"}, Format: tar.FormatPAX,
	}); err != nil {
		t.Fatal(err)
	}
	contents := []byte("data")
	if err := writer.WriteHeader(&tar.Header{Name: "dir/file.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(contents); err != nil {
		t.Fatal(err)
	}
	withSymlink := runtime.GOOS != "windows"
	if withSymlink {
		if err := writer.WriteHeader(&tar.Header{Name: "dir/link", Typeflag: tar.TypeSymlink, Linkname: "file.txt"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}

	destination := t.TempDir()
	if err := extractTarGzip(data.Bytes(), destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "dir", "file.txt")); err != nil {
		t.Fatal(err)
	}
	if withSymlink {
		if resolved, err := os.ReadFile(filepath.Join(destination, "dir", "link")); err != nil || string(resolved) != "data" {
			t.Fatalf("symlink contents = %q, %v", resolved, err)
		}
	}
}

func TestTarGzipExtractorRejectsEscapingSymlink(t *testing.T) {
	var data bytes.Buffer
	compressor := gzip.NewWriter(&data)
	writer := tar.NewWriter(compressor)
	if err := writer.WriteHeader(&tar.Header{Name: "dir/link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGzip(data.Bytes(), t.TempDir()); err == nil {
		t.Fatal("escaping tar symlink was accepted")
	}
}
