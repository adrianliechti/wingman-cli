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
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v4"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	graphtool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/graph"
	lsptool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/lsp"
	toolmcp "github.com/adrianliechti/wingman-agent/pkg/agent/tool/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/browser"
	"github.com/adrianliechti/wingman-agent/pkg/changes"
	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/debugadapter"
	"github.com/adrianliechti/wingman-agent/pkg/graph"
	"github.com/adrianliechti/wingman-agent/pkg/language"
	"github.com/adrianliechti/wingman-agent/pkg/layout"
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

	MCP     *mcp.Manager
	Browser *browser.Service

	Language *language.Service
	DAP      *dap.Manager
	Changes  *changes.Manager

	warmupOnce sync.Once

	mu               sync.RWMutex
	closed           bool
	warmed           bool
	mcpToolsByServer map[string][]tool.Tool
	lspTools         []tool.Tool
	graphTools       []tool.Tool

	mcpCatalogMu     sync.Mutex
	mcpRefreshCancel context.CancelFunc

	// LSP calls may include server startup and network round-trips. Keep their
	// lifetime lock separate from workspace state so close does not block
	// unrelated workspace readers.
	lspLifeMu sync.RWMutex
	dapLifeMu sync.RWMutex

	memoryMu          sync.Mutex
	memoryCache       string
	memoryFingerprint string
}

func NewWorkspace(workDir string) (*Workspace, error) {
	workDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}

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

	workspace := &Workspace{
		Root:        root,
		RootPath:    workDir,
		MemoryPath:  memoryDir,
		ScratchPath: scratchDir,
		Skills:      mergedSkills,
		Plugins:     plugins,
		MCP:         mcpManager,
	}
	workspace.Browser = browser.NewService(mcpManager, workDir)
	return workspace, nil
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
		var changesManager *changes.Manager
		if isGitRepo(w.RootPath) {
			changesManager = changes.New(w.RootPath)
		}

		debugRegistry := debugadapter.NewRegistry()
		var languageOptions []lsp.ManagerOption
		if bundles := debugRegistry.JDTLSBundles(); len(bundles) > 0 {
			languageOptions = append(languageOptions, lsp.WithServerInitializationOptions("jdtls", map[string]any{
				"bundles": bundles,
			}))
		}
		languageService := language.New(w.RootPath, filepath.Join(projectGraphDir(w.RootPath), "graph.json"), languageOptions...)
		dapManager := dap.NewManager(w.RootPath, debugRegistry.Descriptors()...)
		dapManager.SetAdapterConnector(debugadapter.NewConnector(languageService))
		graphEngine := languageService.Graph()
		lspTools := lsptool.NewTools(languageService)
		graphTools := graphtool.NewTools(graphEngine)

		lspTools = w.protectLSPTools(lspTools)

		w.lspLifeMu.Lock()
		w.dapLifeMu.Lock()
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			w.dapLifeMu.Unlock()
			w.lspLifeMu.Unlock()
			dapManager.Close()
			languageService.Close()
			if changesManager != nil {
				changesManager.Close()
			}
			return
		}
		w.Changes = changesManager
		w.Language = languageService
		w.DAP = dapManager
		w.lspTools = lspTools
		w.graphTools = graphTools
		w.warmed = true
		w.mu.Unlock()
		w.dapLifeMu.Unlock()
		w.lspLifeMu.Unlock()

		languageService.WarmUp()
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
	w.dapLifeMu.Lock()
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.dapLifeMu.Unlock()
		w.lspLifeMu.Unlock()
		return
	}
	w.closed = true
	mcpManager := w.MCP
	browserService := w.Browser
	languageService := w.Language
	dapManager := w.DAP
	changesManager := w.Changes
	root := w.Root
	scratchPath := w.ScratchPath
	mcpRefreshCancel := w.mcpRefreshCancel
	w.MCP = nil
	w.Browser = nil
	w.Language = nil
	w.DAP = nil
	w.Changes = nil
	w.mcpToolsByServer = nil
	w.mcpRefreshCancel = nil
	w.lspTools = nil
	w.graphTools = nil
	w.mu.Unlock()
	if browserService != nil {
		browserService.SetChangeHandler(nil)
	}
	if mcpManager != nil {
		mcpManager.SetToolListChangedHandler(nil)
	}
	if mcpRefreshCancel != nil {
		mcpRefreshCancel()
	}
	// Java's debug adapter is hosted by JDT LS, so DAP must disconnect while
	// the language service is still available.
	if dapManager != nil {
		dapManager.Close()
	}
	if languageService != nil {
		languageService.Close()
	}
	w.dapLifeMu.Unlock()
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
	available := !w.closed && w.warmed
	w.mu.RUnlock()
	if !available {
		return
	}

	var nextChanges *changes.Manager
	if isGitRepo(w.RootPath) {
		nextChanges = changes.New(w.RootPath)
	}

	w.mu.Lock()
	if w.closed || !w.warmed {
		w.mu.Unlock()
		if nextChanges != nil {
			nextChanges.Close()
		}
		return
	}
	previousChanges := w.Changes
	w.Changes = nextChanges
	w.mu.Unlock()
	if previousChanges != nil {
		previousChanges.Close()
	}
}

// SyncEditorDocument drives the explicit editor document lifecycle. Feature
// requests still carry the current buffer as a recovery mechanism, but normal
// synchronization no longer depends on the user invoking a language feature.
func (w *Workspace) SyncEditorDocument(ctx context.Context, filePath, content string, saved bool) error {
	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	if service == nil {
		return nil
	}
	return service.SyncDocument(ctx, filePath, content, saved)
}

func (w *Workspace) CloseEditorDocument(ctx context.Context, filePath string) error {
	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	if service == nil {
		return nil
	}
	return service.CloseDocument(ctx, filePath)
}

func (w *Workspace) EditorLSPCapabilities(ctx context.Context, filePath string) (lsp.ServerCapabilities, bool, error) {
	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	if service == nil {
		return lsp.ServerCapabilities{}, false, nil
	}
	return service.Capabilities(ctx, filePath)
}

func (w *Workspace) EditorLSPDocumentContent(filePath string) (string, bool) {
	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	if service == nil {
		return "", false
	}
	return service.DocumentContent(filePath)
}

func (w *Workspace) Diagnostics(ctx context.Context) language.WorkspaceReport {
	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	if service == nil {
		return language.WorkspaceReport{}
	}
	return service.Diagnostics(ctx)
}

// FileDiagnostics returns diagnostics for one disk file or in-memory editor
// buffer. The boolean is false when the server has not produced a result yet.
func (w *Workspace) FileDiagnostics(ctx context.Context, filePath string, content *string) ([]language.Diagnostic, bool, error) {
	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	if service == nil {
		return nil, false, fmt.Errorf("language service unavailable")
	}
	return service.FileDiagnostics(ctx, filePath, content)
}

func (w *Workspace) languageService() (*language.Service, error) {
	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	if service == nil {
		return nil, fmt.Errorf("language service unavailable")
	}
	return service, nil
}

func (w *Workspace) GraphEngine() (*graph.Engine, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.Graph(), nil
}

func (w *Workspace) GraphStateDir() string {
	return projectGraphDir(w.RootPath)
}

// DefinitionLocations resolves a position in a disk file or in-memory editor
// buffer using the language server associated with the file, falling back to
// the tree-sitter graph index when no server covers it.
func (w *Workspace) DefinitionLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.Location, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.DefinitionLocations(ctx, filePath, content, line, column)
}

func (w *Workspace) TypeDefinitionLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.Location, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.TypeDefinitionLocations(ctx, filePath, content, line, column)
}

func (w *Workspace) ImplementationLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.Location, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.ImplementationLocations(ctx, filePath, content, line, column)
}

func (w *Workspace) ReferenceLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.Location, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.ReferenceLocations(ctx, filePath, content, line, column)
}

func (w *Workspace) HoverInformation(ctx context.Context, filePath string, content *string, line, column int) (string, error) {
	service, err := w.languageService()
	if err != nil {
		return "", err
	}
	return service.Hover(ctx, filePath, content, line, column)
}

// CompletionItems asks the file's language server first and falls back to
// symbols extracted from the current buffer with tree-sitter when completion
// is unavailable.
func (w *Workspace) CompletionItems(ctx context.Context, filePath string, content *string, line, column int, completionContext *lsp.CompletionContext) (lsp.CompletionList, error) {
	service, err := w.languageService()
	if err != nil {
		return lsp.CompletionList{}, err
	}
	return service.CompletionItems(ctx, filePath, content, line, column, completionContext)
}

func (w *Workspace) ResolveCompletionItem(ctx context.Context, filePath string, content *string, item lsp.CompletionItem) (lsp.CompletionItem, error) {
	service, err := w.languageService()
	if err != nil {
		return lsp.CompletionItem{}, err
	}
	return service.ResolveCompletionItem(ctx, filePath, content, item)
}

func (w *Workspace) SignatureHelp(ctx context.Context, filePath string, content *string, line, column int, signatureContext *lsp.SignatureHelpContext) (*lsp.SignatureHelp, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.SignatureHelp(ctx, filePath, content, line, column, signatureContext)
}

func (w *Workspace) PrepareRename(ctx context.Context, filePath string, content *string, line, column int) (lsp.PrepareRenameResult, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.PrepareRename(ctx, filePath, content, line, column)
}

func (w *Workspace) Rename(ctx context.Context, filePath string, content *string, line, column int, newName string) (*lsp.WorkspaceEdit, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.Rename(ctx, filePath, content, line, column, newName)
}

func (w *Workspace) CodeActions(
	ctx context.Context,
	filePath string,
	content *string,
	selection lsp.Range,
	only []lsp.CodeActionKind,
	trigger lsp.CodeActionTriggerKind,
) ([]lsp.CommandOrCodeAction, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.CodeActions(ctx, filePath, content, selection, only, trigger)
}

func (w *Workspace) ResolveCodeAction(ctx context.Context, filePath string, content *string, action lsp.CodeAction) (*lsp.CodeAction, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.ResolveCodeAction(ctx, filePath, content, action)
}

func (w *Workspace) ExecuteLSPCommand(ctx context.Context, filePath string, content *string, command lsp.Command) (any, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.ExecuteCommand(ctx, filePath, content, command)
}

func (w *Workspace) Formatting(ctx context.Context, filePath string, content *string, options lsp.FormattingOptions) ([]lsp.TextEdit, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.Formatting(ctx, filePath, content, options)
}

func (w *Workspace) RangeFormatting(ctx context.Context, filePath string, content *string, selection lsp.Range, options lsp.FormattingOptions) ([]lsp.TextEdit, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.RangeFormatting(ctx, filePath, content, selection, options)
}

func (w *Workspace) OnTypeFormatting(ctx context.Context, filePath string, content *string, line, column int, character string, options lsp.FormattingOptions) ([]lsp.TextEdit, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.OnTypeFormatting(ctx, filePath, content, line, column, character, options)
}

func (w *Workspace) InlayHints(ctx context.Context, filePath string, content *string, selection lsp.Range) ([]lsp.InlayHint, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.InlayHints(ctx, filePath, content, selection)
}

func (w *Workspace) DocumentSymbolItems(ctx context.Context, filePath string, content *string) ([]lsp.DocumentSymbol, []lsp.SymbolInformation, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, nil, err
	}
	return service.DocumentSymbols(ctx, filePath, content)
}

func (w *Workspace) DocumentHighlights(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.DocumentHighlight, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.DocumentHighlights(ctx, filePath, content, line, column)
}

func (w *Workspace) FoldingRanges(ctx context.Context, filePath string, content *string) ([]lsp.FoldingRange, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.FoldingRanges(ctx, filePath, content)
}

func (w *Workspace) SemanticTokens(ctx context.Context, filePath string, content *string) ([]language.SemanticToken, error) {
	service, err := w.languageService()
	if err != nil {
		return nil, err
	}
	return service.SemanticTokens(ctx, filePath, content)
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
		wrapped[i].Execute = func(ctx context.Context, args map[string]any) (tool.Result, error) {
			out, err := execute(ctx, args)
			if err != nil {
				return out, err
			}
			path, _ := args["file_path"].(string)
			if note := w.postEditDiagnostics(ctx, path); note != "" {
				out.Content += "\n\n" + note
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

	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	if service == nil {
		return ""
	}
	return service.PostEditDiagnostics(ctx, path)
}

func (w *Workspace) protectLSPTools(tools []tool.Tool) []tool.Tool {
	protected := append([]tool.Tool(nil), tools...)
	for i := range protected {
		execute := protected[i].Execute
		if execute == nil {
			continue
		}
		protected[i].Execute = func(ctx context.Context, args map[string]any) (tool.Result, error) {
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

func (w *Workspace) GitInit() error {
	if isGitRepo(w.RootPath) {
		return errors.New("workspace is already a Git repository")
	}
	if err := removeDanglingGitPointer(w.RootPath); err != nil {
		return err
	}
	if _, err := git.PlainInitWithOptions(w.RootPath, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.Main},
	}); err != nil {
		return fmt.Errorf("initialize Git repository: %w", err)
	}
	w.SyncProjectMode()
	return nil
}

func removeDanglingGitPointer(dir string) error {
	path := filepath.Join(dir, git.GitDirName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect .git: %w", err)
	}
	if info.IsDir() {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("workspace contains an unusable .git file: %w", err)
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return errors.New("workspace contains a .git file that is not a Git repository; remove it and try again")
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	if _, err := os.Stat(target); err == nil {
		return errors.New("workspace .git file points to an existing external Git directory; remove it manually to reinitialize")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale .git file: %w", err)
	}
	return nil
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

func (w *Workspace) GitHistory(ctx context.Context) ([]changes.GitCommit, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return nil, changes.ErrNotGitRepository
	}
	return w.Changes.History(ctx)
}

func (w *Workspace) GitCompare(ctx context.Context, base, head string, mergeBase bool) (changes.CompareResult, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.Changes == nil {
		return changes.CompareResult{}, changes.ErrNotGitRepository
	}
	return w.Changes.Compare(ctx, base, head, mergeBase)
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

// WithDAPManager keeps the workspace's debugger alive for the duration of fn.
// DAP is an editor service, not an agent tool; callers use this boundary from
// explicit user-facing launch and inspection workflows.
func (w *Workspace) WithDAPManager(fn func(*dap.Manager) error) error {
	w.dapLifeMu.RLock()
	defer w.dapLifeMu.RUnlock()

	w.mu.RLock()
	manager := w.DAP
	closed := w.closed
	w.mu.RUnlock()
	if closed || manager == nil {
		return errors.New("debug service unavailable")
	}
	return fn(manager)
}

func (w *Workspace) HasLSP() bool {
	w.mu.RLock()
	service := w.Language
	w.mu.RUnlock()
	return service != nil && service.HasLSP()
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
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
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
	path, _ := layout.WingmanPath("mcp.json")
	return path
}

func projectStateDir(workingDir string) string {
	path, err := layout.WingmanPath("projects", projectKey(workingDir))
	if err == nil {
		return path
	}

	return filepath.Join(os.TempDir(), ".wingman", "projects", projectKey(workingDir))
}

func projectMemoryDir(workingDir string) string {
	return filepath.Join(projectStateDir(workingDir), "memory")
}

func projectGraphDir(workingDir string) string {
	return filepath.Join(projectStateDir(workingDir), "graph")
}

// projectPluginDataDir holds PLUGIN_DATA for plugins installed in the project,
// alongside that project's other state so it survives plugin updates.
func projectPluginDataDir(workingDir string) string {
	return filepath.Join(projectStateDir(workingDir), "plugin-data")
}

func personalPluginDataDir() string {
	path, _ := layout.WingmanPath("plugin-data")
	return path
}

func SessionsDir(workingDir string) string {
	return filepath.Join(projectStateDir(workingDir), "sessions")
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
