package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"go.lsp.dev/protocol"
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

func TestFindCommandIn(t *testing.T) {
	dir := t.TempDir()
	name := "gopls"
	if runtime.GOOS == "windows" {
		name = "gopls.exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findCommandIn([]string{t.TempDir(), dir}, "gopls"); got != path {
		t.Fatalf("findCommandIn = %q, want %q", got, path)
	}

	if got := findCommandIn([]string{dir}, "rust-analyzer"); got != "" {
		t.Fatalf("findCommandIn for missing command = %q, want empty", got)
	}
}

func TestResolveCommandFindsVenvServers(t *testing.T) {
	binSub := filepath.Join(".venv", "bin")
	fileName := "pylsp"
	if runtime.GOOS == "windows" {
		binSub = filepath.Join(".venv", "Scripts")
		fileName = "pylsp.exe"
	}

	root := t.TempDir()
	proj := filepath.Join(root, "services", "api")
	binDir := filepath.Join(proj, binSub)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(binDir, fileName)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := resolveCommand(proj, root, "pylsp"); got != path {
		t.Fatalf("resolveCommand = %q, want %q", got, path)
	}

	// A venv at the workspace root serves nested project dirs via walk-up.
	rootBin := filepath.Join(root, binSub)
	if err := os.MkdirAll(rootBin, 0o755); err != nil {
		t.Fatal(err)
	}
	rootServer := filepath.Join(rootBin, "basedpyright-langserver")
	if runtime.GOOS == "windows" {
		rootServer += ".cmd"
	}
	if err := os.WriteFile(rootServer, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := resolveCommand(proj, root, "basedpyright-langserver"); got != rootServer {
		t.Fatalf("walk-up resolveCommand = %q, want %q", got, rootServer)
	}
}

func TestCommandCandidates(t *testing.T) {
	got := commandCandidates("windows", "gopls")
	want := []string{"gopls.exe", "gopls.cmd", "gopls.bat", "gopls"}
	if len(got) != len(want) {
		t.Fatalf("windows candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("windows candidates = %v, want %v", got, want)
		}
	}

	if got := commandCandidates("darwin", "gopls"); len(got) != 1 || got[0] != "gopls" {
		t.Fatalf("darwin candidates = %v, want [gopls]", got)
	}
}

func TestFindCommandInIgnoresNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bits are not checked on windows")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gopls"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "rust-analyzer"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findCommandIn([]string{dir}, "gopls"); got != "" {
		t.Fatalf("non-executable file resolved: %q", got)
	}
	if got := findCommandIn([]string{dir}, "rust-analyzer"); got != "" {
		t.Fatalf("directory resolved: %q", got)
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
	if !serverVersionSupported(server, command) {
		t.Fatal("TypeScript 7 should satisfy a version 7 minimum")
	}
	server.MinimumMajorVersion = 8
	if serverVersionSupported(server, command) {
		t.Fatal("TypeScript 7 should not satisfy a version 8 minimum")
	}
	if !serverVersionSupported(Server{}, filepath.Join(dir, "missing")) {
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

	roots := detectAll(root)
	if len(roots) != 1 {
		t.Fatalf("detected roots = %+v, want one TypeScript root", roots)
	}
	if roots[0].Server.Name != "typescript-go" || roots[0].Server.Command != tsc {
		t.Fatalf("detected server = %+v, want native TypeScript server %q", roots[0].Server, tsc)
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
