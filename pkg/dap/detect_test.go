package dap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestDetectProjectsUsesDescriptorMarkersAndSkipsGeneratedTrees(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "project.marker"), "root\n")
	writeTestFile(t, filepath.Join(root, "services", "api", "project.marker"), "nested\n")
	writeTestFile(t, filepath.Join(root, "vendor", "dep", "project.marker"), "ignored\n")
	writeTestFile(t, filepath.Join(root, ".cache", "dep", "project.marker"), "ignored\n")

	projects, err := detectProjects(context.Background(), root, []string{"project.marker"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root, filepath.Join(root, "services", "api")}
	if len(projects) != len(want) {
		t.Fatalf("projects = %v, want %v", projects, want)
	}
	for index := range want {
		if projects[index] != want[index] {
			t.Fatalf("projects = %v, want %v", projects, want)
		}
	}
}

func TestDetectProjectsUsesSourceFileAsWorkspaceFallback(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "tools", "main.py"), "print('hello')\n")

	projects, err := detectProjects(context.Background(), root, []string{"pyproject.toml"}, []string{".py"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projects, []string{root}) {
		t.Fatalf("projects = %v, want workspace root", projects)
	}
}

func TestDetectProjectsDoesNotAddFallbackBesideNestedMarker(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "services", "api")
	writeTestFile(t, filepath.Join(project, "api.csproj"), "<Project />\n")
	writeTestFile(t, filepath.Join(project, "Program.cs"), "Console.WriteLine(\"ready\");\n")

	projects, err := detectProjects(context.Background(), root, []string{"*.csproj"}, []string{".cs"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projects, []string{project}) {
		t.Fatalf("projects = %v, want only %v", projects, project)
	}
}

func TestDetectProjectsKeepsFallbackForSourcesOutsideNestedMarker(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "services", "api")
	writeTestFile(t, filepath.Join(project, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(project, "main.py"), "print('nested')\n")
	writeTestFile(t, filepath.Join(root, "tools.py"), "print('root')\n")

	projects, err := detectProjects(context.Background(), root, []string{"pyproject.toml"}, []string{".py"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root, project}
	if !reflect.DeepEqual(projects, want) {
		t.Fatalf("projects = %v, want %v", projects, want)
	}
}

func TestResolvePlanPassesAdapterConfigurationThrough(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "service")
	writeTestFile(t, filepath.Join(project, "go.mod"), "module example.com/service\n")
	program := filepath.Join(project, "cmd", "app")
	if err := os.MkdirAll(program, 0o755); err != nil {
		t.Fatal(err)
	}
	selected := detectedAdapter{
		adapter: AdapterDescriptor{
			Name: "delve", AdapterID: "go",
			Defaults: map[string]any{"type": "go"},
		},
		projects: []string{project},
	}
	configuration := map[string]any{
		"mode":        "debug",
		"program":     program,
		"stopOnEntry": true,
		"custom":      map[string]any{"kept": true},
	}
	plan, err := resolvePlan(root, selected, StartOptions{Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProjectDir != project || plan.Target != program || plan.Mode != "debug" || plan.Request != "launch" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Arguments["type"] != "go" || !reflect.DeepEqual(plan.Arguments["custom"], configuration["custom"]) || plan.Arguments["request"] != "launch" {
		t.Fatalf("arguments = %+v", plan.Arguments)
	}
	if _, changed := configuration["name"]; changed {
		t.Fatal("resolvePlan mutated the caller configuration")
	}
}

func TestResolvePlanMapsIOAndUsesAdapterTargetKey(t *testing.T) {
	root := t.TempDir()
	selected := detectedAdapter{
		adapter: AdapterDescriptor{
			Name:            "codelldb",
			TargetConfigKey: "mainClass",
			IOConfigKey:     "terminal",
			IOValues: map[IOMode]string{
				IOOutput:   "console",
				IOTerminal: "integrated",
			},
			TerminalStrategy: TerminalRunInTerminal,
		},
		projects: []string{root},
	}
	plan, err := resolvePlan(root, selected, StartOptions{
		IO:            IOTerminal,
		Configuration: map[string]any{"mainClass": "demo.App"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target != "demo.App" || plan.Arguments["terminal"] != "integrated" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestResolvePlanSelectsNestedProjectFromProgram(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "services", "api")
	program := filepath.Join(nested, "cmd", "api")
	if err := os.MkdirAll(program, 0o755); err != nil {
		t.Fatal(err)
	}
	selected := detectedAdapter{adapter: AdapterDescriptor{Name: "test"}, projects: []string{root, nested}}

	plan, err := resolvePlan(root, selected, StartOptions{Configuration: map[string]any{"program": program}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProjectDir != nested {
		t.Fatalf("project dir = %q, want %q", plan.ProjectDir, nested)
	}
}

func TestResolvePlanResolvesOnlyDescriptorPathFields(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "cmd", "app")
	if err := os.MkdirAll(program, 0o755); err != nil {
		t.Fatal(err)
	}
	selected := detectedAdapter{
		adapter: AdapterDescriptor{
			Name: "test",
			ConfigurationPaths: []ConfigurationPath{
				{Key: "program"},
				{Key: "cwd", Directory: true},
			},
		},
		projects: []string{root},
	}
	configuration := map[string]any{
		"program": "cmd/app",
		"cwd":     ".",
		"module":  "cmd/app",
	}

	plan, err := resolvePlan(root, selected, StartOptions{Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Arguments["program"] != program || plan.Arguments["cwd"] != root {
		t.Fatalf("resolved arguments = %#v", plan.Arguments)
	}
	if plan.Arguments["module"] != "cmd/app" {
		t.Fatalf("opaque adapter field was changed: %#v", plan.Arguments["module"])
	}
	if configuration["program"] != "cmd/app" || configuration["cwd"] != "." {
		t.Fatalf("caller configuration was mutated: %#v", configuration)
	}
}

func TestWorkspacePathsRejectSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if _, err := ResolveWorkspaceDirectory(root, "escape"); err == nil {
		t.Fatal("symlinked project outside the workspace was accepted")
	}
	if _, err := ResolveConfigurationPaths(root, root, []ConfigurationPath{{Key: "program", Directory: true}}, map[string]any{"program": "escape"}); err == nil {
		t.Fatal("symlinked configuration path outside the workspace was accepted")
	}
}

func TestConfigurationPathCanAllowMissingBuildOutput(t *testing.T) {
	root := t.TempDir()
	configuration, err := ResolveConfigurationPaths(root, root, []ConfigurationPath{{Key: "program", AllowMissing: true}}, map[string]any{
		"program": filepath.Join("bin", "Debug", "net8.0", "app.dll"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "bin", "Debug", "net8.0", "app.dll")
	if configuration["program"] != want {
		t.Fatalf("program = %#v, want %q", configuration["program"], want)
	}
}

func TestMissingConfigurationPathRejectsSymlinkedParentEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "bin")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := ResolveConfigurationPaths(root, root, []ConfigurationPath{{Key: "program", AllowMissing: true}}, map[string]any{
		"program": filepath.Join("bin", "Debug", "app.dll"),
	}); err == nil {
		t.Fatal("missing path below an escaping symlink was accepted")
	}
}

func TestMissingConfigurationPathRejectsFileAsParent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "bin"), "not a directory\n")
	if _, err := ResolveConfigurationPaths(root, root, []ConfigurationPath{{Key: "program", AllowMissing: true}}, map[string]any{
		"program": filepath.Join("bin", "app.dll"),
	}); err == nil {
		t.Fatal("missing path below a regular file was accepted")
	}
}

func TestMergeSourceBreakpointsPreservesUserBreakpoint(t *testing.T) {
	planned := []SourceBreakpoint{{Line: 18}, {Line: 30}}
	editor := []SourceBreakpoint{{Line: 18, Condition: "ready"}, {Line: 24}}
	merged := mergeSourceBreakpoints(planned, editor)
	want := []SourceBreakpoint{{Line: 18, Condition: "ready"}, {Line: 24}, {Line: 30}}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged breakpoints = %#v, want %#v", merged, want)
	}
	if planned[0].Condition != "" {
		t.Fatalf("planned breakpoints were mutated: %#v", planned)
	}
}

func TestManagerRejectsSecondActiveSession(t *testing.T) {
	manager := newManager(t.TempDir(), nil, func(string) string { return "" }, nil)
	manager.session = &Session{id: "active", state: StateRunning}

	if _, err := manager.Start(context.Background(), StartOptions{}); !errors.Is(err, ErrActiveSession) {
		t.Fatalf("Start error = %v, want ErrActiveSession", err)
	}
}

func TestResolveCodeLLDBFindsEditorExtension(t *testing.T) {
	home := t.TempDir()
	name := "codelldb"
	if runtime.GOOS == "windows" {
		name = "codelldb.exe"
	}
	path := filepath.Join(home, ".vscode", "extensions", "vadimcn.vscode-lldb-1.11.0", "adapter", name)
	writeTestFile(t, path, "adapter\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveCodeLLDB(home); got != path {
		t.Fatalf("resolveCodeLLDB = %q, want %q", got, path)
	}
}

func TestManagerFindsAdapterInNestedProjectEnvironment(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "services", "api")
	writeTestFile(t, filepath.Join(project, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(project, "main.py"), "print('ready')\n")
	binDir := "bin"
	adapterName := "debugpy-adapter"
	if runtime.GOOS == "windows" {
		binDir = "Scripts"
		adapterName += ".exe"
	}
	adapterPath := filepath.Join(project, ".venv", binDir, adapterName)
	writeTestFile(t, adapterPath, "adapter\n")
	if err := os.Chmod(adapterPath, 0o755); err != nil {
		t.Fatal(err)
	}

	manager := newManager(root, []AdapterDescriptor{{
		Name: "debugpy", Command: "debugpy-adapter",
		Markers: []string{"pyproject.toml"}, SourceExtensions: []string{".py"},
	}}, func(string) string { return "" }, nil)
	values, err := manager.detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].adapter.Command != adapterPath {
		t.Fatalf("detected adapters = %#v, want nested command %q", values, adapterPath)
	}
}

func TestManagerStopClearsTheOnlySession(t *testing.T) {
	manager := newManager(t.TempDir(), nil, func(string) string { return "" }, nil)
	session := &Session{
		id:           "finished",
		state:        StateTerminated,
		stop:         &Stop{Reason: "breakpoint"},
		pending:      make(map[int]chan responseResult),
		stateChanged: make(chan struct{}),
		launchDone:   make(chan struct{}),
	}
	manager.session = session

	if err := manager.Stop(context.Background(), "finished"); err != nil {
		t.Fatal(err)
	}
	if manager.ActiveSession() != nil {
		t.Fatal("stopped session remained active")
	}
	status := session.Status()
	if status.Stop != nil || status.Error != "" {
		t.Fatalf("normal stop status = %+v", status)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
