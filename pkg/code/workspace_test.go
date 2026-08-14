package code

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	wingmcp "github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
)

func TestWarmUpCreatesLSPManagerOutsideGitRepository(t *testing.T) {
	w, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.WarmUp()
	if w.Language == nil {
		t.Fatal("language service was not created")
	}
}

func TestWorkspaceRefreshesMCPToolsAfterListChanged(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	addTestMCPTool(server, "first")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}

	manager := wingmcp.NewManager(&wingmcp.Config{Servers: map[string]wingmcp.ServerConfig{}})
	manager.AddSession("dynamic", clientSession)
	w := &Workspace{MCP: manager}
	defer w.Close()

	if err := w.InitMCP(ctx); err != nil {
		t.Fatal(err)
	}

	initial, _, _ := w.ManagedTools()
	if got := mcpToolNames(initial); !slices.Equal(got, []string{"dynamic_first"}) {
		t.Fatalf("initial MCP tools = %v", got)
	}

	addTestMCPTool(server, "second")
	w.scheduleMCPToolRefresh(context.Background(), manager, "dynamic")
	waitForMCPToolNames(t, w, "dynamic_first", "dynamic_second")

	if got := mcpToolNames(initial); !slices.Equal(got, []string{"dynamic_first"}) {
		t.Fatalf("captured tool snapshot changed in place: %v", got)
	}

	server.RemoveTools("first")
	w.scheduleMCPToolRefresh(context.Background(), manager, "dynamic")
	waitForMCPToolNames(t, w, "dynamic_second")
}

func addTestMCPTool(server *sdkmcp.Server, name string) {
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: name},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{}, nil, nil
		})
}

func mcpToolNames(tools []tool.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

func waitForMCPToolNames(t *testing.T, w *Workspace, want ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tools, _, _ := w.ManagedTools()
		got := mcpToolNames(tools)
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP tools = %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWithEditDiagnosticsWrapsOnlyEditAndWrite(t *testing.T) {
	w := &Workspace{RootPath: t.TempDir()}

	execute := func(ctx context.Context, args map[string]any) (tool.Result, error) {
		return tool.Text("diff output"), nil
	}
	tools := w.WithEditDiagnostics([]tool.Tool{
		{Name: "edit", Execute: execute},
		{Name: "write", Execute: execute},
		{Name: "read", Execute: execute},
	})

	for _, tl := range tools {
		out, err := tl.Execute(context.Background(), map[string]any{"file_path": "main.go"})
		if err != nil {
			t.Fatalf("%s: %v", tl.Name, err)
		}
		if out.Content != "diff output" {
			t.Fatalf("%s output = %q, want passthrough with no LSP manager", tl.Name, out.Content)
		}
	}
}

func TestProtectedLSPCallDoesNotHoldWorkspaceStateLock(t *testing.T) {
	w := &Workspace{}
	started := make(chan struct{})
	release := make(chan struct{})
	tools := w.protectLSPTools([]tool.Tool{{
		Name: "lsp",
		Execute: func(context.Context, map[string]any) (tool.Result, error) {
			close(started)
			<-release
			return tool.Text("ok"), nil
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
	scratch := t.TempDir()
	skills, err := loadBundledSkills(scratch)
	if err != nil {
		t.Fatalf("loadBundledSkills: %v", err)
	}

	names := make(map[string]bool, len(skills))
	for _, sk := range skills {
		names[sk.Name] = true
		if !sk.Bundled {
			t.Errorf("skill %q should be marked bundled", sk.Name)
		}
		if strings.TrimSpace(sk.Content) == "" {
			t.Errorf("skill %q has empty content", sk.Name)
		}
		switch sk.Name {
		case "architecture", "code-review", "commit", "debug", "feature-dev", "patch", "pull-request", "security-review", "test", "threat-model", "triage", "vuln-scan":
			if !strings.Contains(sk.Content, "$ARGUMENTS") {
				t.Errorf("argument-taking skill %q does not expose Claude-compatible $ARGUMENTS", sk.Name)
			}
		}
		if sk.Name == "skill-creator" {
			for _, resource := range []string{"assets/.gitignore", "assets/SKILL.template.md", "references/skill-format.md"} {
				if _, err := os.Stat(filepath.Join(sk.Location, filepath.FromSlash(resource))); err != nil {
					t.Errorf("skill-creator resource %q was not copied: %v", resource, err)
				}
			}
			if !strings.HasPrefix(sk.Location, filepath.Join(scratch, "skills")+string(filepath.Separator)) {
				t.Errorf("skill-creator location %q is outside managed scratch skills", sk.Location)
			}
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
		"skill-creator",
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

func TestPersonalSkillOverridesManagedBundledSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	personalDir := filepath.Join(home, ".wingman", "skills", "skill-creator")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(personalDir, "SKILL.md"), `---
name: skill-creator
description: Personal override
---
Use the personal workflow.`)

	bundled, err := loadBundledSkills(t.TempDir())
	if err != nil {
		t.Fatalf("loadBundledSkills: %v", err)
	}
	personal, err := skill.DiscoverPersonal()
	if err != nil {
		t.Fatalf("DiscoverPersonal: %v", err)
	}
	merged := skill.Merge(bundled, personal)
	override := skill.FindSkill("skill-creator", merged)
	if override == nil || override.Bundled || override.Description != "Personal override" {
		t.Fatalf("personal override was not selected: %#v", override)
	}
	if _, err := os.Stat(filepath.Join(home, ".wingman", "skills", ".system")); !os.IsNotExist(err) {
		t.Fatalf("bundled snapshot leaked into personal discovery root: %v", err)
	}
}

func TestNewWorkspaceLoadsPluginComponents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	work := t.TempDir()

	pluginDir := filepath.Join(work, ".wingman", "plugins", "reports")
	if err := os.MkdirAll(filepath.Join(pluginDir, "skills", "summarize"), 0755); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, filepath.Join(pluginDir, "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "reports"
}`)
	mustWrite(t, filepath.Join(pluginDir, "skills", "summarize", "SKILL.md"), `---
name: summarize
description: Summarize a report
---
Summarize it.`)
	mustWrite(t, filepath.Join(pluginDir, "mcp.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "reporting": {"type": "streamable-http", "url": "https://reports.example/mcp"}
  }
}`)

	ws, err := NewWorkspace(work)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	if len(ws.Plugins) != 1 || ws.Plugins[0].Name != "reports" {
		t.Fatalf("plugins = %#v", ws.Plugins)
	}

	summarize := skill.FindSkill("summarize", ws.Skills)
	if summarize == nil || summarize.Plugin != "reports" {
		t.Fatalf("plugin skill was not merged: %#v", summarize)
	}

	if skill.FindSkill("reports:summarize", ws.Skills) == nil {
		t.Fatalf("qualified plugin skill is not resolvable")
	}

	if ws.MCP == nil {
		t.Fatalf("plugin MCP server did not create a manager")
	}

	if server, ok := ws.MCP.Servers["reporting"]; !ok || server.URL != "https://reports.example/mcp" {
		t.Fatalf("servers = %#v", ws.MCP.Servers)
	}

	prompt := skill.FormatForPrompt(ws.Skills)
	if !strings.Contains(prompt, "<name>reports:summarize</name>") {
		t.Fatalf("plugin skill is not advertised to the model:\n%s", prompt)
	}
	if !strings.Contains(prompt, filepath.ToSlash(summarize.Location)+"/SKILL.md") {
		t.Fatalf("plugin skill location is not advertised:\n%s", prompt)
	}
	if !filepath.IsAbs(summarize.Location) {
		t.Fatalf("plugin skill location %q must be absolute so the file tools can read it", summarize.Location)
	}
}

func TestNewWorkspaceProjectConfigOverridesPluginServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	work := t.TempDir()

	pluginDir := filepath.Join(work, ".wingman", "plugins", "reports")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, filepath.Join(pluginDir, "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "reports"
}`)
	mustWrite(t, filepath.Join(pluginDir, "mcp.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "reporting": {"type": "streamable-http", "url": "https://plugin.example/mcp"}
  }
}`)
	mustWrite(t, filepath.Join(work, "mcp.json"), `{
  "mcpServers": {
    "reporting": {"url": "https://project.example/mcp"}
  }
}`)

	ws, err := NewWorkspace(work)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	if server := ws.MCP.Servers["reporting"]; server.URL != "https://project.example/mcp" {
		t.Fatalf("server = %#v, want the project config to win", server)
	}
}

func TestNewWorkspaceDedupesPluginServerMatchingProjectConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	work := t.TempDir()

	pluginDir := filepath.Join(work, ".wingman", "plugins", "reports")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, filepath.Join(pluginDir, "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "reports"
}`)
	mustWrite(t, filepath.Join(pluginDir, "mcp.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "from-plugin": {"type": "streamable-http", "url": "https://same.example/mcp"}
  }
}`)
	mustWrite(t, filepath.Join(work, "mcp.json"), `{
  "mcpServers": {
    "from-project": {"url": "https://same.example/mcp"}
  }
}`)

	ws, err := NewWorkspace(work)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	if len(ws.MCP.Servers) != 1 {
		t.Fatalf("servers = %#v, want the duplicate endpoint collapsed", ws.MCP.Servers)
	}

	if _, ok := ws.MCP.Servers["from-project"]; !ok {
		t.Fatalf("servers = %#v, want the configured name kept", ws.MCP.Servers)
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

func TestIsGitRepoDiscoversParent(t *testing.T) {
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, false); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(dir, "nested", "workspace")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if !isGitRepo(subdir) {
		t.Fatal("repository subdirectory was not detected")
	}
}
