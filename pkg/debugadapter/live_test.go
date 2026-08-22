package debugadapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/devtools"
)

func TestLiveDebugpyLaunch(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real debugpy session")
	}
	root := t.TempDir()
	program := filepath.Join(root, "main.py")
	if err := os.WriteFile(program, []byte(`def work(value):
    return value + 1

print(work(41))
`), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry(nil)
	manager := dap.NewManager(root, registry.Descriptors()...)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	adapters, err := manager.Adapters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	available := false
	for _, adapter := range adapters {
		if adapter.Name == "debugpy" {
			available = true
			break
		}
	}
	if !available {
		t.Skip("debugpy is not installed")
	}

	session, err := manager.Start(ctx, dap.StartOptions{
		Adapter:    "debugpy",
		ProjectDir: root,
		Configuration: map[string]any{
			"type":       "python",
			"program":    program,
			"cwd":        root,
			"justMyCode": true,
		},
		Breakpoints: map[string][]dap.SourceBreakpoint{
			program: {{Line: 2}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.State != dap.StateStopped {
		status, _ = session.WaitForStop(ctx, session.StateEpoch())
	}
	if status.State != dap.StateStopped {
		t.Fatalf("status = %+v\noutput:\n%s", status, session.Output())
	}
	frames, _, err := session.StackTrace(ctx, 0, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 || frames[0].Source == nil || filepath.Clean(frames[0].Source.Path) != program {
		t.Fatalf("frames = %+v", frames)
	}
	if err := manager.Stop(ctx, session.ID()); err != nil {
		t.Fatal(err)
	}
}

func TestLiveViteLaunchStartsPackageScript(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real browser debug session")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "debug", "react-vite"))
	if err != nil {
		t.Fatal(err)
	}
	tools, err := devtools.New()
	if err != nil {
		t.Fatal(err)
	}
	if tools.Resolve("js-debug-adapter") == "" || tools.Resolve("chrome-for-testing") == "" {
		t.Skip("managed JavaScript debugger and Chrome for Testing are required")
	}
	registry := NewRegistry(nil)
	profile, err := registry.Plan(javascriptLanguage, Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: root,
		BrowserExecutable: tools.Resolve("chrome-for-testing"),
		Target:            Target{Name: "dev", Kind: "browser-script", Language: javascriptLanguage, Path: filepath.Join(root, "package.json")},
	})
	if err != nil {
		t.Fatal(err)
	}
	profile.Configuration["runtimeArgs"] = []string{"--headless=new"}
	manager := dap.NewManager(root, registry.Descriptors()...)
	manager.SetCommandResolver(tools.Resolve)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	appSource := filepath.Join(root, "src", "main.tsx")
	session, err := manager.Start(ctx, dap.StartOptions{
		Adapter: "vscode-js-debug", ProjectDir: root, Request: profile.Request,
		Configuration: profile.Configuration, IO: profile.IO, PreLaunch: profile.PreLaunch,
		Breakpoints: map[string][]dap.SourceBreakpoint{appSource: {{Line: 5}}},
	})
	if err != nil {
		t.Fatalf("start Vite browser session: %v\noutput:\n%s", err, failedSessionOutput(manager))
	}
	if output := session.Output(); !strings.Contains(output, "Development server is ready") || !strings.Contains(output, "VITE") {
		t.Fatalf("Vite was not started by the session:\n%s", output)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer stopCancel()
	status := session.Status()
	if status.State != dap.StateStopped {
		status, _ = session.WaitForStop(stopCtx, status.StateVersion)
	}
	if status.State != dap.StateStopped {
		t.Fatalf("App breakpoint was not reached: %+v\noutput:\n%s", status, session.Output())
	}
	frames, _, err := session.StackTrace(stopCtx, status.Stop.ThreadID, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 || frames[0].Source == nil || filepath.Clean(frames[0].Source.Path) != appSource || frames[0].Line != 5 {
		t.Fatalf("breakpoint frames = %+v", frames)
	}
	if err := manager.Stop(ctx, session.ID()); err != nil {
		t.Fatal(err)
	}
}

func TestLiveNodePackageScript(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real Node debug session")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"server":"node server.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "server.js")
	if err := os.WriteFile(program, []byte(`const message = "wingman-node-package-ready"
console.log(message)
setInterval(() => {}, 1000)
`), 0o644); err != nil {
		t.Fatal(err)
	}
	tools, err := devtools.New()
	if err != nil {
		t.Fatal(err)
	}
	if tools.Resolve("js-debug-adapter") == "" {
		t.Skip("managed JavaScript debugger is required")
	}
	registry := NewRegistry(nil)
	profile, err := registry.Plan(javascriptLanguage, Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: root,
		Target: Target{Name: "server", Kind: "node-script", Language: javascriptLanguage, Path: filepath.Join(root, "package.json")},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := dap.NewManager(root, registry.Descriptors()...)
	manager.SetCommandResolver(tools.Resolve)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := manager.Start(ctx, dap.StartOptions{
		Adapter: "vscode-js-debug", ProjectDir: root, Request: profile.Request,
		Configuration: profile.Configuration, IO: profile.IO,
		Breakpoints: map[string][]dap.SourceBreakpoint{program: {{Line: 2}}},
	})
	if err != nil {
		t.Fatalf("start Node package script: %v\noutput:\n%s", err, failedSessionOutput(manager))
	}
	status := session.Status()
	if status.State != dap.StateStopped {
		status, _ = session.WaitForStop(ctx, status.StateVersion)
	}
	if status.State != dap.StateStopped {
		t.Fatalf("Node package script breakpoint was not reached: %+v\noutput:\n%s", status, session.Output())
	}
	frames, _, err := session.StackTrace(ctx, status.Stop.ThreadID, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 || frames[0].Source == nil || !sameFile(frames[0].Source.Path, program) || frames[0].Line != 2 {
		t.Fatalf("breakpoint frames = %+v", frames)
	}
	if err := manager.Stop(ctx, session.ID()); err != nil {
		t.Fatal(err)
	}
}

func sameFile(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}

func failedSessionOutput(manager *dap.Manager) string {
	if session := manager.ActiveSession(); session != nil {
		return session.Output()
	}
	return ""
}

func TestLiveNetCoreDbgAutoBuildLaunch(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real NetCoreDbg session")
	}
	if absoluteCommandPath("dotnet") == "" {
		t.Skip("dotnet is not installed")
	}
	t.Setenv("WINGMAN_HOME", t.TempDir())
	tools, err := devtools.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if _, err := tools.Update(ctx, []devtools.Requirement{{Alternatives: []string{"netcoredbg"}}}); err != nil {
		t.Fatal(err)
	}
	if tools.Resolve("netcoredbg") == "" {
		t.Fatal("managed netcoredbg was not installed")
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>net8.0</TargetFramework>
    <RollForward>LatestMajor</RollForward>
    <ImplicitUsings>enable</ImplicitUsings>
  </PropertyGroup>
</Project>
`
	source := `var value = Work(41);
Console.WriteLine(value);

static int Work(int input)
{
    return input + 1;
}
`
	if err := os.WriteFile(filepath.Join(root, "LiveDebug.csproj"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Program.cs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry(nil)
	profile, err := registry.Plan(dotnetLanguage, Request{
		Action: "debug", WorkspaceDir: root, ProjectDir: ".",
		Target: Target{Name: "Program", Kind: "main", Language: dotnetLanguage, Path: "Program.cs", Directory: ".", Line: 1, Column: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.PreLaunch == nil || !profile.PreLaunch.WaitForExit {
		t.Fatalf("unbuilt project did not plan an automatic build: %+v", profile.PreLaunch)
	}

	manager := dap.NewManager(root, registry.Descriptors()...)
	manager.SetCommandResolver(tools.Resolve)
	defer manager.Close()
	session, err := manager.Start(ctx, dap.StartOptions{
		Adapter:       "netcoredbg",
		ProjectDir:    ".",
		Configuration: profile.Configuration,
		PreLaunch:     profile.PreLaunch,
		Breakpoints: map[string][]dap.SourceBreakpoint{
			filepath.Join(root, "Program.cs"): {{Line: 2}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.State != dap.StateStopped {
		status, _ = session.WaitForStop(ctx, 0)
	}
	if status.State != dap.StateStopped {
		t.Fatalf("status = %+v\noutput:\n%s", status, session.Output())
	}
	frames, _, err := session.StackTrace(ctx, 0, 0, 5)
	if err != nil || len(frames) == 0 {
		t.Fatalf("frames = %+v, %v", frames, err)
	}
	evaluation, err := session.Evaluate(ctx, "value", frames[0].ID)
	if err != nil || strings.TrimSpace(evaluation.Result) != "42" {
		t.Fatalf("value = %+v, %v\noutput:\n%s", evaluation, err, session.Output())
	}
	if err := session.Continue(ctx, 0); err != nil {
		t.Fatal(err)
	}
	status, _ = session.WaitForStop(ctx, status.StateVersion)
	if status.State != dap.StateTerminated {
		t.Fatalf("status after continue = %+v", status)
	}
	if !strings.Contains(session.Output(), "42") {
		t.Fatalf("program output missing:\n%s", session.Output())
	}
}
