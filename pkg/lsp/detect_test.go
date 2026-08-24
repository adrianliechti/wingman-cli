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
)

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

func TestDetectAllPrefersNativeTypeScriptSevenLSP(t *testing.T) {
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
	roots := detectAll(root, func(command string) string {
		if command == "tsc" {
			return managed
		}
		return ""
	})
	if len(roots) != 1 {
		t.Fatalf("detected roots = %+v, want one TypeScript root", roots)
	}
	if roots[0].Server.Name != "typescript-go" || roots[0].Server.Command != tsc {
		t.Fatalf("detected server = %+v, want native TypeScript server %q", roots[0].Server, tsc)
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

func TestDetectRequirementsRecognizesSourceAndGradleMarkers(t *testing.T) {
	for _, test := range []struct {
		name    string
		marker  string
		source  string
		project string
		command string
	}{
		{name: "shell script", marker: "scripts/build.sh", project: "bash", command: "bash-language-server"},
		{name: "YAML document", marker: "config/release.yml", project: "yaml", command: "yaml-language-server"},
		{name: "PHP Composer project", marker: "composer.json", project: "php", command: "intelephense"},
		{name: "Java Gradle settings", marker: "settings.gradle", project: "java", command: "jdtls"},
		{name: "Kotlin Gradle project", marker: "build.gradle.kts", source: "src/main/kotlin/example/App.kt", project: "kotlin", command: "kotlin-lsp"},
		{name: "Kotlin Maven project", marker: "pom.xml", source: "src/main/kotlin/example/App.kt", project: "kotlin", command: "kotlin-lsp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(test.marker))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if test.source != "" {
				source := filepath.Join(root, filepath.FromSlash(test.source))
				if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, []byte("fun main() {}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			for _, requirement := range DetectRequirements(root) {
				if requirement.Project == test.project && slices.Contains(requirement.Commands, test.command) {
					return
				}
			}
			t.Fatalf("%s requirement not detected: %+v", test.project, DetectRequirements(root))
		})
	}
}

func TestWorkspaceScopedLanguagesUseOneRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"scripts/build.sh", "examples/run.sh", "config/app.yml", "deploy/app.yaml"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	requirements := DetectRequirements(root)
	for _, project := range []string{"bash", "yaml"} {
		var matches []string
		for _, requirement := range requirements {
			if requirement.Project == project {
				matches = requirement.Directories
				break
			}
		}
		if !reflect.DeepEqual(matches, []string{root}) {
			t.Errorf("%s directories = %v, want one workspace root %q", project, matches, root)
		}
	}
}

func TestKotlinRequirementNeedsKotlinSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "build.gradle.kts"), []byte("plugins { java }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, requirement := range DetectRequirements(root) {
		if requirement.Project == "kotlin" {
			t.Fatalf("Java-only Gradle project detected as Kotlin: %+v", DetectRequirements(root))
		}
	}
}

func TestDetectAllUsesManagedServerAsFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("PATH", "")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed", "gopls")
	roots := detectAll(root, func(command string) string {
		if command == "gopls" {
			return managed
		}
		return ""
	})
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
	roots := detectAll(root, func(command string) string {
		if command == "gopls" {
			return managed
		}
		return ""
	})
	if len(roots) != 1 || roots[0].Server.Command != managed {
		t.Fatalf("roots = %+v, want managed command %q", roots, managed)
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
	if len(entries) != 1 || entries[0].path != filepath.Join(root, "package.json") {
		t.Fatalf("indexed package.json entries = %+v, want only the root marker", entries)
	}
	if !index.hasChild(root, "package.json") {
		t.Fatal("root project marker was not indexed as a child of the root")
	}
}

func TestIndexWorkspaceKeepsHiddenDirectoryMarker(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, ".metals")
	if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, entry := range indexWorkspace(root).matching(".metals") {
		if entry.path == marker && entry.isDir {
			return
		}
	}
	t.Fatalf("hidden directory marker %q was not recorded", marker)
}

func TestHasNestedFileMatchesFilesOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "component.vue"), 0o755); err != nil {
		t.Fatal(err)
	}

	index := indexWorkspace(root)
	if index.hasNestedFile(root, []string{"*.vue"}) {
		t.Fatal("directory should not satisfy a source-file requirement")
	}

	nested := filepath.Join(root, "src")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "component.vue"), []byte("<template/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !indexWorkspace(root).hasNestedFile(root, []string{"*.vue"}) {
		t.Fatal("nested source file should satisfy requirement")
	}
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
