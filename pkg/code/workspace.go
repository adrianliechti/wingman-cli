package code

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v4"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	graphtool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/graph"
	lsptool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/lsp"
	toolmcp "github.com/adrianliechti/wingman-agent/pkg/agent/tool/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/changes"
	"github.com/adrianliechti/wingman-agent/pkg/graph"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/plugin"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
	"github.com/adrianliechti/wingman-agent/pkg/text"
)

//go:embed all:skills
var bundledFS embed.FS

const memoryMaxBytes = 25 * 1024

type UI interface {
	Elicit(ctx context.Context, req tool.ElicitRequest) (tool.ElicitResult, error)
	Confirm(ctx context.Context, message string) (bool, error)
}

type sessionCtxKey struct{}

func WithSessionID(ctx context.Context, sid string) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sid)
}

func SessionIDFromContext(ctx context.Context) string {
	sid, _ := ctx.Value(sessionCtxKey{}).(string)
	return sid
}

type Workspace struct {
	Root        *os.Root
	RootPath    string
	MemoryPath  string
	ScratchPath string

	Skills []skill.Skill

	Plugins []plugin.Plugin

	MCP *mcp.Manager

	LSP     *lsp.Manager
	Changes *changes.Manager
	Graph   *graph.Engine

	warmupOnce sync.Once

	mu               sync.RWMutex
	closed           bool
	mcpToolsByServer map[string][]tool.Tool
	lspTools         []tool.Tool
	graphTools       []tool.Tool

	mcpCatalogMu     sync.Mutex
	mcpRefreshCancel context.CancelFunc

	// LSP calls may include server startup and network round-trips. Keep their
	// lifetime lock separate from workspace state so close does not block
	// unrelated workspace readers.
	lspLifeMu sync.RWMutex

	memoryMu          sync.Mutex
	memoryCache       string
	memoryFingerprint string
}

func NewWorkspace(workDir string) (*Workspace, error) {
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}

	scratchDir := filepath.Join(os.TempDir(), "wingman-"+uuid.New().String())
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		root.Close()
		return nil, fmt.Errorf("create scratch directory: %w", err)
	}
	bundled, err := loadBundledSkills(scratchDir)
	if err != nil {
		os.RemoveAll(scratchDir)
		root.Close()
		return nil, fmt.Errorf("load bundled skills: %w", err)
	}

	memoryDir := projectMemoryDir(workDir)
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		os.RemoveAll(scratchDir)
		root.Close()
		return nil, fmt.Errorf("create memory directory: %w", err)
	}

	plugins, diagnostics := plugin.Discover(workDir, projectPluginDataDir(workDir), personalPluginDataDir())
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(os.Stderr, "plugin: %s\n", diagnostic)
	}

	personal := skill.MustDiscoverPersonal()
	discovered := skill.MustDiscover(workDir)
	mergedSkills := skill.Merge(skill.Merge(skill.Merge(bundled, plugin.Skills(plugins)), personal), discovered)
	reportShadowedPluginSkills(mergedSkills)

	mcpManager := loadMCP(workDir, plugins)

	return &Workspace{
		Root:        root,
		RootPath:    workDir,
		MemoryPath:  memoryDir,
		ScratchPath: scratchDir,
		Skills:      mergedSkills,
		Plugins:     plugins,
		MCP:         mcpManager,
	}, nil
}

// loadMCP layers the project and global configs over the servers plugins
// contribute, then drops entries that duplicate an endpoint already configured
// so its tools are not offered twice.
func loadMCP(workDir string, plugins []plugin.Plugin) *mcp.Manager {
	servers, diagnostics := plugin.Servers(plugins)
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(os.Stderr, "plugin: %s\n", diagnostic)
	}

	manager, _ := mcp.Load(globalMCPConfigPath(), filepath.Join(workDir, "mcp.json"))

	if manager == nil {
		if len(servers) == 0 {
			return nil
		}

		manager = mcp.NewManager(&mcp.Config{Servers: map[string]mcp.ServerConfig{}})
	}

	configured := slices.Sorted(maps.Keys(manager.Servers))

	for _, name := range slices.Sorted(maps.Keys(servers)) {
		if _, ok := manager.Servers[name]; ok {
			fmt.Fprintf(os.Stderr, "plugin: MCP server %q is shadowed by your own configuration\n", name)
			continue
		}

		manager.Servers[name] = servers[name]
	}

	for _, note := range mcp.Dedup(manager.Servers, configured) {
		fmt.Fprintf(os.Stderr, "mcp: %s\n", note)
	}

	manager.Dir = workDir
	return manager
}

func reportShadowedPluginSkills(skills []skill.Skill) {
	for i := range skills {
		if skills[i].Plugin == "" {
			continue
		}

		if winner := skill.FindSkill(skills[i].Name, skills); winner != &skills[i] {
			fmt.Fprintf(os.Stderr, "plugin: skill %q is shadowed; invoke it as /%s\n", skills[i].Name, skills[i].Qualified())
		}
	}
}

func (w *Workspace) WarmUp() {
	w.warmupOnce.Do(func() {
		nativeGit := isGitRepo(w.RootPath)
		changesManager := changes.New(w.RootPath, w.nextShadowGitDir(), nativeGit)

		lspManager := lsp.NewManager(w.RootPath)
		lspTools := lsptool.NewTools(lspManager)

		graphEngine := graph.New(w.RootPath, filepath.Join(projectGraphDir(w.RootPath), "graph.json"), graph.WithResolver(&lspResolver{ws: w}))
		graphTools := graphtool.NewTools(graphEngine)

		lspTools = w.protectLSPTools(lspTools)

		w.lspLifeMu.Lock()
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			w.lspLifeMu.Unlock()
			if lspManager != nil {
				lspManager.Close()
			}
			changesManager.Close()
			return
		}
		w.Changes = changesManager
		w.LSP = lspManager
		w.lspTools = lspTools
		w.Graph = graphEngine
		w.graphTools = graphTools
		w.mu.Unlock()
		w.lspLifeMu.Unlock()

		if lspManager != nil {
			lspManager.WarmUpServers()
		}
	})
}

func (w *Workspace) InitMCP(ctx context.Context) error {
	w.mu.RLock()
	manager := w.MCP
	closed := w.closed
	w.mu.RUnlock()
	if manager == nil || closed {
		return nil
	}

	w.mcpCatalogMu.Lock()
	defer w.mcpCatalogMu.Unlock()
	w.startMCPToolRefreshes(manager)
	connectErr := manager.Connect(ctx)

	sessions := manager.Sessions()
	toolsByServer := make(map[string][]tool.Tool)
	toolsErrs := []error{connectErr}
	for _, serverName := range slices.Sorted(maps.Keys(sessions)) {
		session := sessions[serverName]
		tools, err := toolmcp.ToolsForServer(ctx, serverName, session)
		if err != nil {
			toolsErrs = append(toolsErrs, fmt.Errorf("list MCP tools from %s: %w", serverName, err))
			continue
		}
		toolsByServer[serverName] = tools
	}

	w.mu.Lock()
	if !w.closed {
		w.mcpToolsByServer = toolsByServer
	}
	w.mu.Unlock()
	return errors.Join(toolsErrs...)
}

func (w *Workspace) startMCPToolRefreshes(manager *mcp.Manager) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	if w.mcpRefreshCancel != nil {
		w.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.mcpRefreshCancel = cancel
	manager.SetToolListChangedHandler(func(serverName string) {
		w.scheduleMCPToolRefresh(ctx, manager, serverName)
	})
	w.mu.Unlock()
}

func (w *Workspace) scheduleMCPToolRefresh(ctx context.Context, manager *mcp.Manager, serverName string) {
	if ctx == nil {
		return
	}
	go w.refreshMCPServerTools(ctx, manager, serverName)
}

func (w *Workspace) refreshMCPServerTools(ctx context.Context, manager *mcp.Manager, serverName string) {
	w.mcpCatalogMu.Lock()
	defer w.mcpCatalogMu.Unlock()
	if ctx.Err() != nil {
		return
	}

	session := manager.Session(serverName)
	if session == nil {
		return
	}

	tools, err := toolmcp.ToolsForServer(ctx, serverName, session)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "warning: failed to refresh tools from MCP server %s: %v\n", serverName, err)
		}
		return
	}
	if manager.Session(serverName) != session {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if w.mcpToolsByServer == nil {
		w.mcpToolsByServer = make(map[string][]tool.Tool)
	}
	w.mcpToolsByServer[serverName] = tools
}

func flattenMCPTools(byServer map[string][]tool.Tool) []tool.Tool {
	var tools []tool.Tool
	for _, serverName := range slices.Sorted(maps.Keys(byServer)) {
		tools = append(tools, byServer[serverName]...)
	}
	return tools
}

func (w *Workspace) Close() {
	w.lspLifeMu.Lock()
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.lspLifeMu.Unlock()
		return
	}
	w.closed = true
	mcpManager := w.MCP
	lspManager := w.LSP
	changesManager := w.Changes
	root := w.Root
	scratchPath := w.ScratchPath
	mcpRefreshCancel := w.mcpRefreshCancel
	w.MCP = nil
	w.LSP = nil
	w.Changes = nil
	w.Graph = nil
	w.mcpToolsByServer = nil
	w.mcpRefreshCancel = nil
	w.lspTools = nil
	w.graphTools = nil
	w.mu.Unlock()
	if mcpManager != nil {
		mcpManager.SetToolListChangedHandler(nil)
	}
	if mcpRefreshCancel != nil {
		mcpRefreshCancel()
	}
	if lspManager != nil {
		lspManager.Close()
	}
	w.lspLifeMu.Unlock()

	if mcpManager != nil {
		mcpManager.Close()
	}
	if changesManager != nil {
		changesManager.Close()
	}
	if scratchPath != "" {
		os.RemoveAll(scratchPath)
	}
	if root != nil {
		root.Close()
	}
}

func (w *Workspace) IsGitRepo() bool { return isGitRepo(w.RootPath) }

func (w *Workspace) SyncProjectMode() {
	w.mu.RLock()
	available := !w.closed && w.Changes != nil
	w.mu.RUnlock()
	if !available {
		return
	}

	nativeGit := isGitRepo(w.RootPath)
	nextChanges := changes.New(w.RootPath, w.nextShadowGitDir(), nativeGit)

	w.mu.Lock()
	if w.closed || w.Changes == nil {
		w.mu.Unlock()
		nextChanges.Close()
		return
	}
	previousChanges := w.Changes
	w.Changes = nextChanges
	w.mu.Unlock()
	previousChanges.Close()
}

func (w *Workspace) Diagnostics(ctx context.Context) lsp.WorkspaceDiagnosticsReport {
	w.lspLifeMu.RLock()
	defer w.lspLifeMu.RUnlock()
	w.mu.RLock()
	manager := w.LSP
	w.mu.RUnlock()
	if manager == nil {
		return lsp.WorkspaceDiagnosticsReport{}
	}
	return manager.CollectAllDiagnostics(ctx)
}

// FileDiagnostics returns diagnostics for one disk file or in-memory editor
// buffer. The boolean is false when the server has not produced a result yet.
func (w *Workspace) FileDiagnostics(ctx context.Context, filePath string, content *string) ([]lsp.Diagnostic, bool, error) {
	w.lspLifeMu.RLock()
	defer w.lspLifeMu.RUnlock()
	w.mu.RLock()
	manager := w.LSP
	w.mu.RUnlock()
	if manager == nil {
		return nil, false, fmt.Errorf("language server unavailable")
	}

	session, err := manager.GetSession(ctx, filePath)
	if err != nil {
		return nil, false, err
	}
	uri, err := syncEditorDocument(ctx, session, filePath, content)
	if err != nil {
		return nil, false, err
	}
	diagnostics, known := session.WaitForDiagnostics(ctx, uri, 2*time.Second)
	return diagnostics, known, nil
}

// DefinitionLocations resolves a position in a disk file or in-memory editor
// buffer using the language server associated with the file.
func (w *Workspace) DefinitionLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.DefLocation, error) {
	return w.locationRequest(ctx, filePath, content, func(session *lsp.Session, uri string) ([]lsp.DefLocation, error) {
		return session.DefinitionLocations(ctx, uri, line, column)
	})
}

func (w *Workspace) TypeDefinitionLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.DefLocation, error) {
	return w.locationRequest(ctx, filePath, content, func(session *lsp.Session, uri string) ([]lsp.DefLocation, error) {
		return session.TypeDefinitionLocations(ctx, uri, line, column)
	})
}

func (w *Workspace) ImplementationLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.DefLocation, error) {
	return w.locationRequest(ctx, filePath, content, func(session *lsp.Session, uri string) ([]lsp.DefLocation, error) {
		return session.ImplementationLocations(ctx, uri, line, column)
	})
}

func (w *Workspace) ReferenceLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.DefLocation, error) {
	return w.locationRequest(ctx, filePath, content, func(session *lsp.Session, uri string) ([]lsp.DefLocation, error) {
		return session.ReferenceLocations(ctx, uri, line, column)
	})
}

func (w *Workspace) locationRequest(ctx context.Context, filePath string, content *string, request func(*lsp.Session, string) ([]lsp.DefLocation, error)) ([]lsp.DefLocation, error) {
	w.lspLifeMu.RLock()
	defer w.lspLifeMu.RUnlock()
	w.mu.RLock()
	manager := w.LSP
	w.mu.RUnlock()
	if manager == nil {
		return nil, fmt.Errorf("language server unavailable")
	}

	session, err := manager.GetSession(ctx, filePath)
	if err != nil {
		return nil, err
	}
	uri, err := syncEditorDocument(ctx, session, filePath, content)
	if err != nil {
		return nil, err
	}
	return request(session, uri)
}

func (w *Workspace) HoverInformation(ctx context.Context, filePath string, content *string, line, column int) (string, error) {
	w.lspLifeMu.RLock()
	defer w.lspLifeMu.RUnlock()
	w.mu.RLock()
	manager := w.LSP
	w.mu.RUnlock()
	if manager == nil {
		return "", fmt.Errorf("language server unavailable")
	}

	session, err := manager.GetSession(ctx, filePath)
	if err != nil {
		return "", err
	}
	uri, err := syncEditorDocument(ctx, session, filePath, content)
	if err != nil {
		return "", err
	}
	return session.HoverInformation(ctx, uri, line, column)
}

func (w *Workspace) DocumentSymbolItems(ctx context.Context, filePath string, content *string) ([]lsp.DocumentSymbol, []lsp.SymbolInformation, error) {
	w.lspLifeMu.RLock()
	defer w.lspLifeMu.RUnlock()
	w.mu.RLock()
	manager := w.LSP
	w.mu.RUnlock()
	if manager == nil {
		return nil, nil, fmt.Errorf("language server unavailable")
	}

	session, err := manager.GetSession(ctx, filePath)
	if err != nil {
		return nil, nil, err
	}
	uri, err := syncEditorDocument(ctx, session, filePath, content)
	if err != nil {
		return nil, nil, err
	}
	return session.DocumentSymbolItems(ctx, uri)
}

func syncEditorDocument(ctx context.Context, session *lsp.Session, filePath string, content *string) (string, error) {
	if content != nil {
		return session.SyncDocument(ctx, filePath, *content)
	}
	return session.OpenDocument(ctx, filePath)
}

func (w *Workspace) WithEditDiagnostics(tools []tool.Tool) []tool.Tool {
	wrapped := append([]tool.Tool(nil), tools...)
	for i := range wrapped {
		if wrapped[i].Name != "edit" && wrapped[i].Name != "write" {
			continue
		}
		execute := wrapped[i].Execute
		if execute == nil {
			continue
		}
		wrapped[i].Execute = func(ctx context.Context, args map[string]any) (string, error) {
			out, err := execute(ctx, args)
			if err != nil {
				return out, err
			}
			path, _ := args["file_path"].(string)
			if note := w.postEditDiagnostics(ctx, path); note != "" {
				out += "\n\n" + note
			}
			return out, nil
		}
	}
	return wrapped
}

func (w *Workspace) postEditDiagnostics(ctx context.Context, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(w.RootPath, path)
	}

	w.lspLifeMu.RLock()
	defer w.lspLifeMu.RUnlock()
	w.mu.RLock()
	manager := w.LSP
	w.mu.RUnlock()
	if manager == nil {
		return ""
	}

	return manager.PostEditDiagnostics(ctx, path)
}

func (w *Workspace) protectLSPTools(tools []tool.Tool) []tool.Tool {
	protected := append([]tool.Tool(nil), tools...)
	for i := range protected {
		execute := protected[i].Execute
		if execute == nil {
			continue
		}
		protected[i].Execute = func(ctx context.Context, args map[string]any) (string, error) {
			w.lspLifeMu.RLock()
			defer w.lspLifeMu.RUnlock()
			return execute(ctx, args)
		}
	}
	return protected
}

func (w *Workspace) Diffs(ctx context.Context) ([]changes.FileDiff, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return nil, nil
	}
	return w.Changes.Diffs(ctx)
}

func (w *Workspace) Diff(ctx context.Context, path string, layer changes.DiffLayer) (changes.FileDiff, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return changes.FileDiff{}, errors.New("change tracking is not available")
	}
	return w.Changes.Diff(ctx, path, layer)
}

func (w *Workspace) RevertChange(ctx context.Context, path string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return errors.New("change tracking is not available")
	}
	return w.Changes.Revert(ctx, path)
}

func (w *Workspace) HasNativeGit() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.Changes != nil && w.Changes.IsNativeGit()
}

func (w *Workspace) GitStatus(ctx context.Context) (changes.GitStatus, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return changes.GitStatus{}, changes.ErrNotGitRepository
	}
	return w.Changes.GitStatus(ctx)
}

func (w *Workspace) GitBranches(ctx context.Context, refresh bool) ([]changes.GitBranch, string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return nil, "", changes.ErrNotGitRepository
	}
	return w.Changes.Branches(ctx, refresh)
}

func (w *Workspace) GitCreateBranch(ctx context.Context, name string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return changes.ErrNotGitRepository
	}
	return w.Changes.CreateBranch(ctx, name)
}

func (w *Workspace) GitCheckoutBranch(ctx context.Context, name, remote string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return changes.ErrNotGitRepository
	}
	return w.Changes.CheckoutBranch(ctx, name, remote)
}

func (w *Workspace) GitStage(ctx context.Context, paths []string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return changes.ErrNotGitRepository
	}
	return w.Changes.Stage(ctx, paths)
}

func (w *Workspace) GitUnstage(ctx context.Context, paths []string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return changes.ErrNotGitRepository
	}
	return w.Changes.Unstage(ctx, paths)
}

func (w *Workspace) GitCommit(ctx context.Context, message string) (string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return "", changes.ErrNotGitRepository
	}
	return w.Changes.Commit(ctx, message)
}

func (w *Workspace) GitPull(ctx context.Context) (string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return "", changes.ErrNotGitRepository
	}
	return w.Changes.Pull(ctx)
}

func (w *Workspace) GitPush(ctx context.Context) (string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return "", changes.ErrNotGitRepository
	}
	return w.Changes.Push(ctx)
}

func (w *Workspace) nextShadowGitDir() string {
	return filepath.Join(w.ScratchPath, "changes-"+uuid.NewString()+".git")
}

func (w *Workspace) MemoryContent() string {
	w.memoryMu.Lock()
	defer w.memoryMu.Unlock()

	files := listMemoryFiles(w.MemoryPath)

	var fp strings.Builder
	for _, f := range files {
		fmt.Fprintf(&fp, "%s\x00%d\n", f.name, f.mtime.UnixNano())
	}
	if fp.String() == w.memoryFingerprint {
		return w.memoryCache
	}

	var lines []string
	for _, f := range files {
		line := "- " + f.name
		if hook := extractMemoryHook(filepath.Join(w.MemoryPath, f.name)); hook != "" {
			line += " — " + hook
		}
		lines = append(lines, line)
	}

	content := strings.Join(lines, "\n")
	if len(content) > memoryMaxBytes {
		content = text.HeadBytes(content, memoryMaxBytes)
		if idx := strings.LastIndex(content, "\n"); idx > 0 {
			content = content[:idx]
		}
		content += "\n\n> WARNING: memory index exceeded 25KB and was truncated."
	}

	w.memoryCache = content
	w.memoryFingerprint = fp.String()
	return content
}

type memoryFile struct {
	name  string
	mtime time.Time
}

func listMemoryFiles(dir string) []memoryFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var files []memoryFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, memoryFile{name: e.Name(), mtime: info.ModTime()})
	}

	slices.SortFunc(files, func(a, b memoryFile) int { return strings.Compare(a.name, b.name) })
	return files
}

func extractMemoryHook(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := string(data)

	if fmBody, body, ok := splitFrontmatter(text); ok {
		var fm struct {
			Description string `yaml:"description"`
		}
		if err := yaml.Load([]byte(fmBody), &fm); err == nil {
			if d, _, _ := strings.Cut(strings.TrimSpace(fm.Description), "\n"); d != "" {
				return strings.TrimSpace(d)
			}
		}
		text = body
	}

	for line := range strings.SplitSeq(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
	}
	return ""
}

func splitFrontmatter(text string) (fm, body string, ok bool) {
	rest, found := strings.CutPrefix(text, "---\n")
	if !found {
		rest, found = strings.CutPrefix(text, "---\r\n")
	}
	if !found {
		return "", "", false
	}
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return "", "", false
	}
	body = strings.TrimLeft(after, "\r\n")
	return before, body, true
}

func (w *Workspace) ManagedTools() (mcpTools, lspTools, graphTools []tool.Tool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	mcpTools = flattenMCPTools(w.mcpToolsByServer)
	lspTools = append([]tool.Tool(nil), w.lspTools...)
	graphTools = append([]tool.Tool(nil), w.graphTools...)
	return
}

func (w *Workspace) HasLSP() bool {
	w.lspLifeMu.RLock()
	defer w.lspLifeMu.RUnlock()
	w.mu.RLock()
	manager := w.LSP
	w.mu.RUnlock()
	return manager != nil && len(manager.DetectServers()) > 0
}

func (w *Workspace) HasChanges() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.Changes != nil
}

func (w *Workspace) ChangesFingerprint(ctx context.Context) uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return 0
	}
	return w.Changes.Fingerprint(ctx)
}

func isGitRepo(dir string) bool {
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		return false
	}
	_, err = repo.Worktree()
	return err == nil
}

func projectKey(workingDir string) string {
	root := findCanonicalGitRoot(workingDir)
	if root == "" {
		root = workingDir
	}

	sanitized := filepath.Clean(root)

	if vol := filepath.VolumeName(sanitized); vol != "" {
		sanitized = strings.TrimPrefix(sanitized, vol)
	}

	sanitized = strings.TrimPrefix(sanitized, string(filepath.Separator))
	sanitized = strings.ReplaceAll(sanitized, string(filepath.Separator), "_")
	sanitized = strings.ToLower(sanitized)

	return sanitized
}

func globalMCPConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".wingman", "mcp.json")
}

func projectMemoryDir(workingDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	return filepath.Join(home, ".wingman", "projects", projectKey(workingDir), "memory")
}

func projectGraphDir(workingDir string) string {
	return filepath.Join(filepath.Dir(projectMemoryDir(workingDir)), "graph")
}

// projectPluginDataDir holds PLUGIN_DATA for plugins installed in the project,
// alongside that project's other state so it survives plugin updates.
func projectPluginDataDir(workingDir string) string {
	return filepath.Join(filepath.Dir(projectMemoryDir(workingDir)), "plugin-data")
}

func personalPluginDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".wingman", "plugin-data")
}

func SessionsDir(workingDir string) string {
	return filepath.Join(filepath.Dir(projectMemoryDir(workingDir)), "sessions")
}

func findCanonicalGitRoot(dir string) string {
	cur := filepath.Clean(dir)
	for {
		gitPath := filepath.Join(cur, ".git")
		info, err := os.Lstat(gitPath)
		if err == nil {
			if info.IsDir() {
				return cur
			}
			return resolveWorktreeRoot(cur, gitPath)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func resolveWorktreeRoot(worktreeDir, gitFile string) string {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}

	const prefix = "gitdir:"
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, prefix) {
		return ""
	}

	gitdir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktreeDir, gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	if data, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common := strings.TrimSpace(string(data))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
		return filepath.Dir(filepath.Clean(common))
	}

	parent := filepath.Dir(filepath.Dir(gitdir))
	if filepath.Base(parent) == ".git" {
		return filepath.Dir(parent)
	}
	return ""
}

func loadBundledSkills(scratchDir string) ([]skill.Skill, error) {
	source, err := fs.Sub(bundledFS, "skills")
	if err != nil {
		return nil, err
	}

	destination := filepath.Join(scratchDir, "skills")
	if err := os.CopyFS(destination, source); err != nil {
		return nil, err
	}

	return skill.LoadBundledAt(source, ".", destination)
}
