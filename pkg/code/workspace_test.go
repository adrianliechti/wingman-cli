package code

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestProtectedLSPCallDoesNotHoldWorkspaceStateLock(t *testing.T) {
	w := &Workspace{}
	started := make(chan struct{})
	release := make(chan struct{})
	tools := w.protectLSPTools([]tool.Tool{{
		Name: "lsp",
		Execute: func(context.Context, map[string]any) (string, error) {
			close(started)
			<-release
			return "ok", nil
		},
	}})

	executed := make(chan struct{})
	go func() {
		_, _ = tools[0].Execute(context.Background(), nil)
		close(executed)
	}()
	<-started

	closed := make(chan struct{})
	go func() {
		w.Close()
		close(closed)
	}()

	// Close waits for the LSP call, but it waits on the dedicated lifecycle
	// lock. Unrelated workspace readers must remain available meanwhile.
	read := make(chan struct{})
	go func() {
		w.mu.RLock()
		w.mu.RUnlock()
		close(read)
	}()
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("workspace state read blocked behind LSP shutdown")
	}
	select {
	case <-closed:
		t.Fatal("workspace closed while an LSP call was active")
	default:
	}

	close(release)
	select {
	case <-executed:
	case <-time.After(time.Second):
		t.Fatal("LSP call did not finish")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("workspace close did not resume")
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "worktrees"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
}

func initWorktree(t *testing.T, mainRoot, worktreeRoot, name string) {
	t.Helper()
	if err := os.MkdirAll(worktreeRoot, 0755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	worktreeGitdir := filepath.Join(mainRoot, ".git", "worktrees", name)
	if err := os.MkdirAll(worktreeGitdir, 0755); err != nil {
		t.Fatalf("mkdir worktree gitdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeRoot, ".git"), []byte("gitdir: "+worktreeGitdir+"\n"), 0644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(worktreeGitdir, "commondir"), []byte("../..\n"), 0644); err != nil {
		t.Fatalf("write commondir: %v", err)
	}
}

func TestFindCanonicalGitRoot_NoGit(t *testing.T) {
	dir := t.TempDir()
	if got := findCanonicalGitRoot(dir); got != "" {
		t.Errorf("expected empty for non-git dir, got %q", got)
	}
}

func TestFindCanonicalGitRoot_PlainRepo(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	if got := findCanonicalGitRoot(dir); got != filepath.Clean(dir) {
		t.Errorf("expected %q, got %q", dir, got)
	}
}

func TestFindCanonicalGitRoot_RepoSubdir(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	sub := filepath.Join(dir, "internal", "foo")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if got := findCanonicalGitRoot(sub); got != filepath.Clean(dir) {
		t.Errorf("expected %q, got %q", dir, got)
	}
}

func TestFindCanonicalGitRoot_WorktreeResolvesToMain(t *testing.T) {
	parent := t.TempDir()
	mainRoot := filepath.Join(parent, "repo")
	worktree := filepath.Join(parent, "worktree-feature")

	if err := os.MkdirAll(mainRoot, 0755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}
	initRepo(t, mainRoot)
	initWorktree(t, mainRoot, worktree, "feature")

	got := findCanonicalGitRoot(worktree)
	if got != filepath.Clean(mainRoot) {
		t.Errorf("expected worktree to resolve to main %q, got %q", mainRoot, got)
	}
}

func TestFindCanonicalGitRoot_WorktreeFallbackWithoutCommondir(t *testing.T) {
	parent := t.TempDir()
	mainRoot := filepath.Join(parent, "repo")
	worktree := filepath.Join(parent, "worktree-feature")

	if err := os.MkdirAll(mainRoot, 0755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}
	initRepo(t, mainRoot)
	initWorktree(t, mainRoot, worktree, "feature")

	if err := os.Remove(filepath.Join(mainRoot, ".git", "worktrees", "feature", "commondir")); err != nil {
		t.Fatalf("remove commondir: %v", err)
	}

	got := findCanonicalGitRoot(worktree)
	if got != filepath.Clean(mainRoot) {
		t.Errorf("expected fallback to resolve to main %q, got %q", mainRoot, got)
	}
}

func TestProjectKey_WorktreesShareKey(t *testing.T) {
	parent := t.TempDir()
	mainRoot := filepath.Join(parent, "repo")
	worktree := filepath.Join(parent, "worktree-feature")

	if err := os.MkdirAll(mainRoot, 0755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}
	initRepo(t, mainRoot)
	initWorktree(t, mainRoot, worktree, "feature")

	mainKey := projectKey(mainRoot)
	worktreeKey := projectKey(worktree)
	subKey := projectKey(filepath.Join(mainRoot, "src"))

	if mainKey != worktreeKey {
		t.Errorf("main and worktree should share key: main=%q worktree=%q", mainKey, worktreeKey)
	}
	if mainKey != subKey {

		if _, err := os.Stat(filepath.Join(mainRoot, "src")); err == nil {
			t.Errorf("subdir should share key with main: main=%q sub=%q", mainKey, subKey)
		}
	}
}

func TestMemoryContent_AutoIndex(t *testing.T) {
	dir := t.TempDir()

	mustWrite(t, filepath.Join(dir, "feedback_testing.md"), `---
name: feedback_testing
description: no DB mocks; real DB only
type: feedback
---

Integration tests must hit a real database.
`)

	mustWrite(t, filepath.Join(dir, "preferences.md"), "# User Preferences\n\n- Likes pi\n")

	mustWrite(t, filepath.Join(dir, "note.md"), "Just a one-liner about something.\n")

	mustWrite(t, filepath.Join(dir, "typed_only.md"), `---
name: typed_only
type: project
---

# Migrate auth middleware
`)

	mustWrite(t, filepath.Join(dir, "notes.txt"), "ignore me\n")

	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	mustWrite(t, filepath.Join(dir, "subdir", "nested.md"), "# Nested\n")

	w := &Workspace{MemoryPath: dir}
	content := w.MemoryContent()

	wantLines := []string{
		"- feedback_testing.md — no DB mocks; real DB only",
		"- note.md — Just a one-liner about something.",
		"- preferences.md — User Preferences",
		"- typed_only.md — Migrate auth middleware",
	}
	for _, want := range wantLines {
		if !strings.Contains(content, want) {
			t.Errorf("expected line %q in index, got:\n%s", want, content)
		}
	}

	if strings.Contains(content, "notes.txt") {
		t.Errorf("non-md file leaked into index:\n%s", content)
	}
	if strings.Contains(content, "subdir") || strings.Contains(content, "nested") {
		t.Errorf("subdir entry leaked into index:\n%s", content)
	}
}

func TestMemoryContent_CacheInvalidatesOnFileChange(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{MemoryPath: dir}

	if got := w.MemoryContent(); got != "" {
		t.Errorf("expected empty index for empty dir, got %q", got)
	}

	mustWrite(t, filepath.Join(dir, "a.md"), "---\ndescription: first\n---\n\nbody\n")
	if got := w.MemoryContent(); !strings.Contains(got, "a.md — first") {
		t.Errorf("expected new file picked up, got %q", got)
	}

	future := time.Now().Add(2 * time.Second)
	mustWrite(t, filepath.Join(dir, "a.md"), "---\ndescription: second\n---\n\nbody\n")
	if err := os.Chtimes(filepath.Join(dir, "a.md"), future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if got := w.MemoryContent(); !strings.Contains(got, "a.md — second") {
		t.Errorf("expected updated description, got %q", got)
	}

	if err := os.Remove(filepath.Join(dir, "a.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := w.MemoryContent(); got != "" {
		t.Errorf("expected empty index after removal, got %q", got)
	}
}

func TestLoadBundledSkillsIncludesCoreWorkflows(t *testing.T) {
	skills := loadBundledSkills()

	names := make(map[string]bool, len(skills))
	for _, sk := range skills {
		names[sk.Name] = true
		if !sk.Bundled {
			t.Errorf("skill %q should be marked bundled", sk.Name)
		}
		if strings.TrimSpace(sk.Content) == "" {
			t.Errorf("skill %q has empty content", sk.Name)
		}
	}

	for _, name := range []string{
		"architecture",
		"code-review",
		"commit",
		"debug",
		"feature-dev",
		"init",
		"memory",
		"patch",
		"pull-request",
		"security-review",
		"simplify",
		"test",
		"threat-model",
		"triage",
		"vuln-scan",
	} {
		if !names[name] {
			t.Errorf("bundled skill %q was not loaded; got %v", name, names)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestProjectKey_NonGitDirUsesRawPath(t *testing.T) {
	dir := t.TempDir()
	key := projectKey(dir)

	if key == "" {
		t.Fatal("expected non-empty key")
	}
	if strings.ContainsRune(key, filepath.Separator) {
		t.Errorf("expected no path separators in key, got %q", key)
	}
	if key != strings.ToLower(key) {
		t.Errorf("expected lowercased key, got %q", key)
	}
}

func TestIsSupportedWorkspace_SmallDir(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "hello")

	if !isSupportedWorkspace(dir) {
		t.Fatal("small dir should be supported")
	}
}

func TestIsSupportedWorkspace_TooManyEntries(t *testing.T) {
	dir := t.TempDir()
	for i := range 20 {
		mustWrite(t, filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), "x")
	}

	prev := workspaceMaxEntries
	workspaceMaxEntries = 10
	defer func() { workspaceMaxEntries = prev }()

	if isSupportedWorkspace(dir) {
		t.Fatal("dir over entry limit should be unsupported")
	}
}

func TestIsSupportedWorkspace_TooManyBytes(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "big.bin"), strings.Repeat("x", 1024))

	prev := workspaceMaxBytes
	workspaceMaxBytes = 512
	defer func() { workspaceMaxBytes = prev }()

	if isSupportedWorkspace(dir) {
		t.Fatal("dir over byte limit should be unsupported")
	}
}

func TestIsSupportedWorkspace_GitRepoAlwaysSupported(t *testing.T) {
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "big.bin"), strings.Repeat("x", 1024))

	prev := workspaceMaxBytes
	workspaceMaxBytes = 512
	defer func() { workspaceMaxBytes = prev }()

	if !isSupportedWorkspace(dir) {
		t.Fatal("git repo should always be supported")
	}
}
