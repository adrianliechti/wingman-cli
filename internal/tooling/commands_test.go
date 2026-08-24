package tooling

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveProjectWalksToWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	name := Candidates(runtime.GOOS, "example-tool")[0]
	command := filepath.Join(root, "node_modules", ".bin", name)
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveProject(project, root, "example-tool"); got != command {
		t.Fatalf("ResolveProject = %q, want %q", got, command)
	}
}

func TestResolverUsesProjectManagedSystemPrecedence(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	name := Candidates(runtime.GOOS, "example-tool")[0]
	projectCommand := filepath.Join(project, "node_modules", ".bin", name)
	if err := os.MkdirAll(filepath.Dir(projectCommand), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectCommand, []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Workspace: root,
		Lookup:    func(string) string { return filepath.Join(root, "system", name) },
		Managed:   func(string) string { return filepath.Join(root, "managed", name) },
	}
	got := resolver.Candidates([]string{project}, "example-tool")
	wantSources := []Source{SourceProject, SourceManaged, SourceSystem}
	if len(got) != len(wantSources) {
		t.Fatalf("candidates = %#v", got)
	}
	for index, source := range wantSources {
		if got[index].Source != source {
			t.Fatalf("candidate sources = %#v, want %v", got, wantSources)
		}
	}
}

func TestRunnableRejectsMissingShebangInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows launchers do not use shebangs")
	}
	command := filepath.Join(t.TempDir(), "broken")
	if err := os.WriteFile(command, []byte("#!/missing/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if Runnable(command) {
		t.Fatal("launcher with a missing interpreter is runnable")
	}
}

func TestEnvironmentPrependsAbsoluteCommandDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "bin")
	command := filepath.Join(directory, "tool")
	environment := Environment(command, []string{"PATH=/usr/bin", "EXAMPLE=1"})
	want := "PATH=" + directory + string(os.PathListSeparator) + "/usr/bin"
	if environment[0] != want {
		t.Fatalf("environment PATH = %q, want %q", environment[0], want)
	}
}

func TestEnvironmentDoesNotAddCurrentDirectoryToEmptyPath(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "bin")
	command := filepath.Join(directory, "tool")
	environment := Environment(command, []string{"PATH="})
	if want := "PATH=" + directory; environment[0] != want {
		t.Fatalf("environment PATH = %q, want %q", environment[0], want)
	}
}

func TestMajorVersionAtLeast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	command := filepath.Join(t.TempDir(), "versioned")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf 'Version 7.1.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !MajorVersionAtLeast(ctx, command, 7, "") || MajorVersionAtLeast(ctx, command, 8, "") {
		t.Fatal("major version probe returned the wrong result")
	}
}

func TestMajorVersionProbeUsesProjectDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".available"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(t.TempDir(), "project-aware")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n[ -f .available ] || exit 1\nprintf 'Version 1.0.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !MajorVersionAtLeast(ctx, command, ProbeExecutes, project) {
		t.Fatal("project-aware command was probed from the wrong directory")
	}
	if MajorVersionAtLeast(ctx, command, ProbeExecutes, t.TempDir()) {
		t.Fatal("project-aware command ignored its working directory")
	}
}

func TestMajorVersionProbeReflectsRuntimeAvailability(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	directory := t.TempDir()
	command := filepath.Join(directory, "runtime-backed")
	marker := command + ".ready"
	contents := "#!/bin/sh\n[ -f \"$0.ready\" ] || exit 1\nprintf 'Version 7.1.0\\n'\n"
	if err := os.WriteFile(command, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !MajorVersionAtLeast(ctx, command, ProbeExecutes, "") {
		t.Fatal("available runtime was rejected")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if MajorVersionAtLeast(ctx, command, ProbeExecutes, "") {
		t.Fatal("probe reused a stale successful result")
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !MajorVersionAtLeast(ctx, command, ProbeExecutes, "") {
		t.Fatal("probe reused a stale failed result")
	}
}

func TestLookPathSearchesEveryGOPATHEntry(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	directory := filepath.Join(second, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	name := Candidates(runtime.GOOS, "wingman-gopath-test")[0]
	command := filepath.Join(directory, name)
	if err := os.WriteFile(command, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", strings.Join([]string{first, second}, string(os.PathListSeparator)))
	t.Setenv("PATH", "")
	got, err := LookPath("wingman-gopath-test")
	if err != nil {
		t.Fatal(err)
	}
	if got != command {
		t.Fatalf("LookPath = %q, want %q", got, command)
	}
}

func TestResolveProjectRejectsDirectoryOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if got := ResolveProject(outside, root, "example-tool"); got != "" {
		t.Fatalf("ResolveProject outside workspace = %q", got)
	}
}

func TestLookPathRejectsMissingAbsoluteCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	if _, err := LookPath(path); err != exec.ErrNotFound {
		t.Fatalf("LookPath error = %v, want exec.ErrNotFound", err)
	}
}

func TestEnvironmentAddsUserDirectoryShebangInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows launchers do not use shebangs")
	}
	interpreterDir := t.TempDir()
	interpreter := filepath.Join(interpreterDir, "wingman-test-interp")
	if err := os.WriteFile(interpreter, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", interpreterDir)

	command := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(command, []byte("#!/usr/bin/env wingman-test-interp\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !Runnable(command) {
		t.Fatal("launcher with a user-directory interpreter is not runnable")
	}
	environment := Environment(command, []string{"PATH=/usr/bin"})
	if !pathContains(strings.TrimPrefix(environment[0], "PATH="), interpreterDir) {
		t.Fatalf("environment PATH %q misses interpreter directory %q", environment[0], interpreterDir)
	}
}

func TestEnvironmentUsesCaseSensitivePathKeyOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows environment keys are case insensitive")
	}
	directory := filepath.Join(t.TempDir(), "bin")
	command := filepath.Join(directory, "tool")
	environment := Environment(command, []string{"Path=/wrong", "PATH=/usr/bin"})
	if environment[0] != "Path=/wrong" {
		t.Fatalf("unrelated Path variable changed to %q", environment[0])
	}
	want := "PATH=" + directory + string(os.PathListSeparator) + "/usr/bin"
	if environment[1] != want {
		t.Fatalf("environment PATH = %q, want %q", environment[1], want)
	}
}
