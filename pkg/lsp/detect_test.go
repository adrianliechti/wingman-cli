package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"go.lsp.dev/protocol"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
	"github.com/adrianliechti/wingman-agent/pkg/devtools"
)

func TestEveryLanguageServerHasManagedInstaller(t *testing.T) {
	t.Setenv("WINGMAN_HOME", t.TempDir())
	tools, err := devtools.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range knownProjects {
		for _, server := range project.Servers {
			if !tools.CanManage(server.Command) {
				t.Errorf("%s registers %s without a managed installer", project.Name, server.Command)
			}
		}
	}
}

func TestManagerInitializationOptionsAreStableSessionIdentity(t *testing.T) {
	manager := NewManager(t.TempDir(), WithServerInitializationOptions("jdtls", map[string]any{
		"bundles": []string{"java-debug.jar"},
	}))
	encoded := manager.initializationOptions["jdtls"]
	var decoded map[string][]string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded["bundles"]; len(got) != 1 || got[0] != "java-debug.jar" {
		t.Fatalf("initialization options = %#v", decoded)
	}
	plain := Server{Command: "jdtls"}
	configured := plain
	configured.InitializationOptions = encoded
	if serverKey(plain) == serverKey(configured) {
		t.Fatal("servers with different initialization options shared an identity")
	}

	wire, err := json.Marshal(struct {
		Options protocol.LSPAny `json:"initializationOptions"`
	}{Options: protocol.LSPAny(encoded)})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Options map[string][]string `json:"initializationOptions"`
	}
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatalf("initialization options were not encoded as an object: %s: %v", wire, err)
	}
	if got := payload.Options["bundles"]; len(got) != 1 || got[0] != "java-debug.jar" {
		t.Fatalf("wire initialization options = %s", wire)
	}
}

func TestServerVersionSupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell script")
	}

	dir := t.TempDir()
	command := filepath.Join(dir, "tsc")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf 'Version 7.1.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	server := Server{MinimumMajorVersion: 7}
	if !serverVersionSupported(server, command, dir) {
		t.Fatal("TypeScript 7 should satisfy a version 7 minimum")
	}
	server.MinimumMajorVersion = 8
	if serverVersionSupported(server, command, dir) {
		t.Fatal("TypeScript 7 should not satisfy a version 8 minimum")
	}
	if !serverVersionSupported(Server{}, filepath.Join(dir, "missing"), dir) {
		t.Fatal("servers without a version constraint should not be probed")
	}
}

type testManagedTools map[string]string

func (m testManagedTools) Resolve(command string) string { return m[command] }

func TestDetectAllChecksManagedTypeScriptVersionWithoutExternalFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses POSIX shell scripts")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tsc := filepath.Join(binDir, "tsc")
	if err := os.WriteFile(tsc, []byte("#!/bin/sh\nprintf 'Version 7.0.2\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	managed := filepath.Join(t.TempDir(), "tsc")
	if err := os.WriteFile(managed, []byte("#!/bin/sh\nprintf 'Version 6.0.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tools := testManagedTools{"tsc": managed, "typescript-language-server": ""}
	if roots := detectAll(root, tools); len(roots) != 0 {
		t.Fatalf("managed TypeScript below the minimum fell back to project server: %+v", roots)
	}
	tools["typescript-language-server"] = filepath.Join(t.TempDir(), "typescript-language-server")
	roots := detectAll(root, tools)
	if len(roots) != 1 || roots[0].Server.Name != "typescript-language-server" || roots[0].Server.Command != tools["typescript-language-server"] {
		t.Fatalf("managed TypeScript 6 should use managed language server: %+v", roots)
	}
	if err := os.WriteFile(managed, []byte("#!/bin/sh\nprintf 'Version 7.0.2\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots = detectAll(root, tools)
	if len(roots) != 1 {
		t.Fatalf("detected roots = %+v, want one TypeScript root", roots)
	}
	if roots[0].Server.Name != "typescript-go" || roots[0].Server.Command != managed {
		t.Fatalf("detected server = %+v, want managed native TypeScript server %q", roots[0].Server, managed)
	}
}

func TestDetectRequirementsDoesNotRequireInstalledServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	requirements := DetectRequirements(root)
	if len(requirements) != 1 || requirements[0].Project != "go" || !reflect.DeepEqual(requirements[0].Commands, []string{"gopls"}) {
		t.Fatalf("requirements = %+v", requirements)
	}
}

func TestDetectRequirementsRecognizesGradleSettings(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "settings.gradle"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	requirements := DetectRequirements(root)
	if len(requirements) != 1 || requirements[0].Project != "java" || !slices.Contains(requirements[0].Commands, "jdtls") {
		t.Fatalf("Java requirement not detected: %+v", requirements)
	}
}

func TestPythonDetectionRequiresTyWithoutFallback(t *testing.T) {
	for _, marker := range []string{"pyproject.toml", "ty.toml", "requirements.txt"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, marker), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			requirements := DetectRequirements(root)
			if len(requirements) != 1 || !reflect.DeepEqual(requirements[0].Commands, []string{"ty"}) {
				t.Fatalf("Python requirements = %+v", requirements)
			}
			tools := testManagedTools{"basedpyright-langserver": filepath.Join(t.TempDir(), "basedpyright-langserver")}
			if roots := detectAll(root, tools); len(roots) != 0 {
				t.Fatalf("Python used another server when ty was missing: %+v", roots)
			}
			tools["ty"] = filepath.Join(t.TempDir(), "ty")
			roots := detectAll(root, tools)
			if len(roots) != 1 || roots[0].Server.Name != "ty" || !reflect.DeepEqual(roots[0].Server.Args, []string{"server"}) {
				t.Fatalf("Python server = %+v", roots)
			}
		})
	}
}

func TestClangdDetectionRoutesSourcesAndHeaders(t *testing.T) {
	for _, marker := range []string{"compile_commands.json", "compile_flags.txt", ".clangd", "CMakeLists.txt", "meson.build"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, marker), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			manager := NewManager(root, WithManagedTools(testManagedTools{"clangd": filepath.Join(t.TempDir(), "clangd")}))
			defer manager.Close()
			for file, language := range map[string]string{"main.c": "c", "main.C": "cpp", "main.cpp": "cpp", "lib.h": "cpp", "lib.hpp": "cpp"} {
				path := filepath.Join(root, "src", file)
				server := manager.FindServer(path)
				if server == nil || server.Name != "clangd" || server.LanguageIDForPath(path) != language {
					t.Errorf("server for %s = %+v, want clangd with language %s", file, server, language)
				}
			}
		})
	}
}

func TestDetectAllUsesManagedServer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("PATH", "")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed", "gopls")
	roots := detectAll(root, testManagedTools{"gopls": managed})
	if len(roots) != 1 || roots[0].Server.Command != managed {
		t.Fatalf("roots = %+v, want managed command %q", roots, managed)
	}
}

func TestDetectAllPrefersManagedServerOverSystemFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	bin := filepath.Join(root, "system-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(bin, tooling.Candidates(runtime.GOOS, "gopls")[0])
	if err := os.WriteFile(system, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed", "gopls")
	roots := detectAll(root, testManagedTools{"gopls": managed})
	if len(roots) != 1 || roots[0].Server.Command != managed {
		t.Fatalf("roots = %+v, want managed command %q", roots, managed)
	}
}

func TestJavaDebugHostUsesOnlyManagedJDTLS(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pom.xml"), []byte("<project/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, tooling.Candidates(runtime.GOOS, "jdtls")[0]), []byte("host"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	managed := filepath.Join(root, "managed", "jdtls")
	tools := testManagedTools{"jdtls": managed}
	manager := NewManager(root, WithManagedTools(tools))
	defer manager.Close()
	roots := manager.detect()
	if len(roots) != 1 || roots[0].Server.Command != managed {
		t.Fatalf("Java hosts = %+v", roots)
	}
	tools["jdtls"] = ""
	manager.InvalidateDetection()
	if roots := manager.detect(); len(roots) != 0 {
		t.Fatalf("unmanaged Java host was accepted: %+v", roots)
	}
}

func TestTypeScriptLanguageIDs(t *testing.T) {
	server := Server{LanguageID: "typescript"}
	tests := map[string]string{
		"file.ts":  "typescript",
		"file.tsx": "typescriptreact",
		"file.js":  "javascript",
		"file.jsx": "javascriptreact",
	}
	for path, want := range tests {
		if got := server.LanguageIDForPath(path); got != want {
			t.Errorf("LanguageIDForPath(%q) = %q, want %q", path, got, want)
		}
	}
	if got := (Server{LanguageID: "go"}).LanguageIDForPath("file.go"); got != "go" {
		t.Fatalf("Go language ID = %q, want go", got)
	}
}

func TestIndexWorkspaceSkipsIgnoredContents(t *testing.T) {
	root := t.TempDir()
	ignored := filepath.Join(root, "node_modules", "pkg")
	if err := os.MkdirAll(ignored, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	index := indexWorkspace(root)
	entries := index.matching("package.json")
	if len(entries) != 1 || entries[0] != filepath.Join(root, "package.json") {
		t.Fatalf("indexed package.json entries = %+v, want only the root marker", entries)
	}
	if !index.hasChild(root, "package.json") {
		t.Fatal("root project marker was not indexed as a child of the root")
	}
}

func TestIndexWorkspaceKeepsHiddenDirectoryMarker(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, ".project")
	if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, path := range indexWorkspace(root).matching(".project") {
		if path == marker {
			return
		}
	}
	t.Fatalf("hidden directory marker %q was not recorded", marker)
}

func TestProjectDirsAppliesExcludes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"package.json", "deno.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	index := indexWorkspace(root)
	for _, project := range knownProjects {
		if project.Name != "typescript" {
			continue
		}
		if dirs := projectDirs(index, project); len(dirs) != 0 {
			t.Fatalf("typescript dirs = %v, want none because deno.json excludes the root", dirs)
		}
		return
	}
	t.Fatal("typescript project type not found")
}

func TestIsSubPathHandlesRelativeRoots(t *testing.T) {
	if !isSubPath(".", filepath.Join("nested", "source.go")) {
		t.Fatal("relative workspace root did not contain a nested file")
	}
	if !isSubPath("workspace", filepath.Join("workspace", "nested", "source.go")) {
		t.Fatal("relative project root did not contain a nested file")
	}
	if isSubPath("workspace", "workspace-other") {
		t.Fatal("path with a shared textual prefix was treated as a child")
	}
}

func TestDetectedProjectsDoNotExposeCachedServerSlices(t *testing.T) {
	manager := &Manager{roots: []projectRoot{{
		Dir: "project",
		Server: Server{
			Args:                  []string{"--stdio"},
			Languages:             []string{"go"},
			InitializationOptions: []byte(`{"setting":true}`),
		},
	}}, detectedAt: time.Now()}

	first := manager.detect()
	first[0].Server.Args[0] = "changed"
	first[0].Server.Languages[0] = "changed"
	first[0].Server.InitializationOptions[0] = 'x'

	second := manager.detect()
	if second[0].Server.Args[0] != "--stdio" || second[0].Server.Languages[0] != "go" || string(second[0].Server.InitializationOptions) != `{"setting":true}` {
		t.Fatalf("cached descriptor was mutated through a detection result: %+v", second[0].Server)
	}
}
