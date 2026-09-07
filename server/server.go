package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/devtools"
	"github.com/adrianliechti/wingman-agent/pkg/remote"
	"github.com/adrianliechti/wingman-agent/pkg/settings"
	"github.com/adrianliechti/wingman-agent/pkg/system"
	"github.com/adrianliechti/wingman-agent/pkg/terminal"
	"github.com/adrianliechti/wingman-agent/pkg/watch"
)

//go:embed static/*
var staticFiles embed.FS

var StaticFS, _ = fs.Sub(staticFiles, "static")

// DefaultPort is the preferred port used when no explicit port is requested.
const DefaultPort = 9000

type ServerOptions struct {
	NoBrowser           bool
	RemoteURL           string
	RemoteToken         string
	disableManagedTools bool
}

type Server struct {
	noBrowser bool

	workspace *code.Workspace
	config    *agent.Config

	ctx     context.Context
	cancel  context.CancelFunc
	mux     chi.Router
	handler http.Handler

	closeOnce  sync.Once
	background sync.WaitGroup

	runtimesMu sync.Mutex
	runtimes   map[string]*backendRuntime
	starting   map[string]*backendStartup
	scope      WorkspaceScope
	wsMu       sync.Mutex
	wsConns    map[*websocket.Conn]*wsClient
	sendMu     sync.Mutex

	lspExternalMu    sync.Mutex
	lspExternalPaths map[string]bool
	fileWriteMu      sync.Mutex

	terminals        *terminal.Manager
	preview          *filePreviewServer
	tab              *editorTabService
	transforms       *editorTransformService
	commitMessages   *gitCommitMessageService
	settingsMu       sync.Mutex
	tabEnabled       atomic.Bool
	terminalPosition atomic.Value
	tabRequestMu     sync.Mutex
	tabRequestID     uint64
	tabRequestCancel context.CancelFunc

	summariesMu sync.Mutex
	summaries   *summaryStore

	files           *watch.Monitor
	skillFiles      *watch.Monitor
	prevGit         bool
	prevLSP         bool
	prevDebug       bool
	prevFingerprint uint64

	managedToolsMu sync.RWMutex
	managedTools   managedToolsStatus
}

func New(ctx context.Context, workDir string, opts *ServerOptions) (*Server, error) {
	if opts == nil {
		opts = new(ServerOptions)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cfg, err := agent.DefaultConfig()
	if err != nil {
		return nil, err
	}
	closeTelemetry := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = cfg.Telemetry.Shutdown(shutdownCtx)
		cancel()
	}
	ws, err := code.NewWorkspace(workDir)
	if err != nil {
		closeTelemetry()
		return nil, err
	}
	serverCtx, cancel := context.WithCancel(ctx)
	s := &Server{
		noBrowser: opts.NoBrowser,
		workspace: ws,
		config:    cfg,
		ctx:       serverCtx,
		cancel:    cancel,
		runtimes:  make(map[string]*backendRuntime),
		scope:     workspaceScope(ws.RootPath),
		wsConns:   map[*websocket.Conn]*wsClient{},
	}

	s.terminals = terminal.NewManager(ws.RootPath)
	s.terminals.SetExitHandler(s.onTerminalExit)
	s.preview, err = newFilePreviewServer(ws.Root)
	if err != nil {
		cancel()
		ws.Close()
		closeTelemetry()
		return nil, err
	}

	wa := codeagent.New(ws, cfg, nil, codeagent.Options{RequireSessionContext: true, IsolateSessionSettings: true})
	rt := s.bindBackend(code.BuiltinAgentName, wa)
	s.runtimes[code.BuiltinAgentName] = rt
	cfg.RoleModel = wa.RoleModel
	cfg.Model = func() string {
		option, _ := wa.RoleModel("")
		return option.ID
	}
	s.tab = newEditorTabService(cfg)
	s.transforms = newEditorTransformService(cfg)
	s.commitMessages = newGitCommitMessageService(cfg)
	s.tabEnabled.Store(true)
	s.terminalPosition.Store(settings.WindowTerminalPositionTab)
	if userSettings, loadErr := settings.Load(); loadErr == nil {
		s.tabEnabled.Store(userSettings.EditorTabCompletion)
		s.terminalPosition.Store(userSettings.WindowTerminalPosition)
	}

	ws.WarmUp()
	if terminal.Supported() {
		_ = ws.WithDAPManager(func(manager *dap.Manager) error {
			manager.SetTerminalLauncher(s)
			return nil
		})
	}

	s.prevGit = ws.IsGitRepo()
	s.prevLSP = ws.HasLSP()
	s.prevDebug = s.debugAvailable(serverCtx)
	s.files = watch.New(watch.Options{Active: s.hasClients}, s.checkWorkspace)
	s.background.Go(func() {
		s.files.Run(serverCtx)
	})
	if !opts.disableManagedTools {
		s.setManagedToolsStatus(managedToolsStatus{State: "installing"})
		update := ws.StartManagedToolsUpdate(serverCtx, code.ManagedLSPTools, func(progress devtools.Progress) {
			s.setManagedToolsStatus(managedToolsStatus{
				State: "installing", Tool: progress.Tool, Label: progress.Label, Phase: progress.Phase,
				Current: progress.Current, Total: progress.Total,
			})
		})
		s.background.Go(func() {
			changed, updateErr := update.WaitContext(serverCtx)
			if serverCtx.Err() != nil {
				return
			}
			if updateErr != nil {
				fmt.Fprintf(os.Stderr, "managed tools warning: %v\n", updateErr)
				if devtools.IsUnavailable(updateErr) {
					s.setManagedToolsStatus(managedToolsStatus{
						State: "error", Error: updateErr.Error(), Unavailable: devtools.ToolLabels(devtools.UnavailableTools(updateErr)),
					})
				} else {
					s.setManagedToolsStatus(managedToolsStatus{State: "ready"})
				}
			} else {
				s.setManagedToolsStatus(managedToolsStatus{State: "ready"})
			}
			if changed && serverCtx.Err() == nil {
				if s.files != nil {
					s.files.Flush()
				}
			}
		})
	}
	s.skillFiles = watch.New(watch.Options{
		Fallback: 2 * time.Second,
		Active:   s.hasClients,
	}, s.refreshSkills)
	s.background.Go(func() {
		s.skillFiles.Run(serverCtx)
	})

	s.background.Go(func() {
		if err := ws.InitMCP(serverCtx); err != nil && serverCtx.Err() == nil {
			fmt.Fprintf(os.Stderr, "MCP init warning: %v\n", err)
		}
	})

	s.background.Go(func() {
		wa.FetchModels(serverCtx)
		if serverCtx.Err() != nil {
			return
		}
		rt.mu.Lock()
		views := make([]*sessionController, 0, len(rt.sessions))
		for _, view := range rt.sessions {
			views = append(views, view)
		}
		rt.mu.Unlock()
		for _, view := range views {
			view.refreshSettings()
		}
		s.broadcast(Frame{Type: EvtModelChanged})
	})

	s.mux = chi.NewRouter()
	s.registerRoutes(s.mux)

	csrf := http.NewCrossOriginProtection()
	s.handler = csrf.Handler(s.checkInstance(s.mux))
	if opts.RemoteURL != "" {
		if err := s.startRemote(remote.ClientOptions{Relay: opts.RemoteURL, Token: opts.RemoteToken}); err != nil {
			s.Close()
			return nil, err
		}
	}

	return s, nil
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}

		s.wsMu.Lock()
		for _, client := range s.wsConns {
			client.close()
			_ = client.conn.CloseNow()
		}
		s.wsMu.Unlock()
		s.runtimesMu.Lock()
		runtimes := make([]*backendRuntime, 0, len(s.runtimes))
		for _, rt := range s.runtimes {
			rt.operationMu.Lock()
			rt.closing = true
			rt.operationMu.Unlock()
			runtimes = append(runtimes, rt)
		}
		s.runtimesMu.Unlock()
		for _, rt := range runtimes {
			rt.turns.SetHandler(nil)
			rt.turns.Close()
			rt.operations.Wait()
			_ = rt.agent.Close()
		}
		s.background.Wait()
		if s.preview != nil {
			s.preview.Close()
		}
		if s.workspace != nil {
			s.workspace.Close()
		}
		if s.terminals != nil {
			s.terminals.Close()
		}
		if s.config != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.config.Telemetry.Shutdown(shutdownCtx)
			cancel()
		}
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) Run(ctx context.Context, port int) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer s.Close()

	resolvedPort, err := resolvePort(port)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf("localhost:%d", resolvedPort),
		Handler: s,
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = srv.Close()
		case <-s.ctx.Done():
			_ = srv.Close()
		case <-done:
		}
	}()

	url := fmt.Sprintf("http://localhost:%d", resolvedPort)
	fmt.Fprintf(os.Stderr, "Wingman running at %s\n", url)

	if !s.noBrowser {
		openBrowser(url)
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) registerRoutes(r chi.Router) {
	r.Route("/api", func(r chi.Router) {
		r.Route("/files", func(r chi.Router) {
			r.Get("/", s.handleFiles)
			r.Post("/", s.handleFileCreate)
			r.Delete("/", s.handleFileDelete)
			r.Get("/read", s.handleFileRead)
			r.Get("/search", s.handleFilesSearch)
			r.Post("/content-search", s.handleWorkspaceSearch)
			r.Get("/path", s.handleFilePath)
			r.Get("/download", s.handleFileDownload)
			r.Get("/preview", s.handleFilePreview)
			r.Post("/reveal", s.handleFileReveal)
			r.Post("/rename", s.handleFileRename)
			r.Post("/copy", s.handleFileCopy)
			r.Post("/write", s.handleFileWrite)
			r.Post("/write-batch", s.handleFileWriteBatch)
		})

		r.Route("/diffs", func(r chi.Router) {
			r.Get("/", s.handleDiffs)
			r.Post("/revert", s.handleDiffRevert)
		})

		r.Route("/git", func(r chi.Router) {
			r.Post("/init", s.handleGitInit)
			r.Get("/status", s.handleGitStatus)
			r.Get("/branches", s.handleGitBranches)
			r.Post("/fetch", s.handleGitFetch)
			r.Get("/history", s.handleGitHistory)
			r.Get("/compare", s.handleGitCompare)
			r.Post("/branches", s.handleGitCreateBranch)
			r.Post("/checkout", s.handleGitCheckoutBranch)
			r.Post("/stage", s.handleGitStage)
			r.Post("/unstage", s.handleGitUnstage)
			r.Post("/commit", s.handleGitCommit)
			r.Post("/commit-message", s.handleGitCommitMessage)
			r.Post("/pull", s.handleGitPull)
			r.Post("/push", s.handleGitPush)
		})

		s.registerSessionRoutes(r)

		r.Route("/terminals", func(r chi.Router) {
			r.Get("/", s.handleTerminals)
			r.Post("/", s.handleNewTerminal)
			r.Get("/shells", s.handleTerminalShells)
			r.Get("/{id}", s.handleTerminal)
			r.Delete("/{id}", s.handleDeleteTerminal)
			r.HandleFunc("/{id}/ws", s.handleTerminalWebSocket)
		})

		r.Route("/graph", func(r chi.Router) {
			r.Get("/overview", s.handleGraphOverview)
			r.Post("/index", s.handleGraphIndex)
			r.Get("/search", s.handleGraphSearch)
			r.Post("/content-search", s.handleGraphContentSearch)
			r.Get("/symbol", s.handleGraphSymbol)
			r.Get("/modules", s.handleGraphModules)
			r.Get("/insights", s.handleGraphInsights)
			r.Post("/summaries", s.handleGraphSummaries)
		})

		r.Route("/lsp", func(r chi.Router) {
			r.Get("/status", s.handleLSPStatus)
			r.Get("/capabilities", s.handleLSPEditorCapabilities)
			r.Post("/document", s.handleLSPDocumentLifecycle)
			r.Get("/diagnostics", s.handleDiagnostics)
			r.Post("/diagnostics", s.handleLSPFileDiagnostics)
			r.Post("/definition", s.handleLSPDefinition)
			r.Post("/type-definition", s.handleLSPTypeDefinition)
			r.Post("/implementations", s.handleLSPImplementations)
			r.Post("/references", s.handleLSPReferences)
			r.Post("/hover", s.handleLSPHover)
			r.Post("/completions", s.handleLSPCompletions)
			r.Post("/completions/resolve", s.handleLSPCompletionResolve)
			r.Post("/signature-help", s.handleLSPSignatureHelp)
			r.Post("/document-symbols", s.handleLSPDocumentSymbols)
			r.Post("/document-highlights", s.handleLSPDocumentHighlights)
			r.Post("/folding-ranges", s.handleLSPFoldingRanges)
			r.Post("/semantic-tokens", s.handleLSPSemanticTokens)
			r.Post("/rename/prepare", s.handleLSPPrepareRename)
			r.Post("/rename", s.handleLSPRename)
			r.Post("/code-actions", s.handleLSPCodeActions)
			r.Post("/code-actions/resolve", s.handleLSPCodeActionResolve)
			r.Post("/execute-command", s.handleLSPExecuteCommand)
			r.Post("/formatting", s.handleLSPFormatting)
			r.Post("/formatting/range", s.handleLSPRangeFormatting)
			r.Post("/formatting/on-type", s.handleLSPOnTypeFormatting)
			r.Post("/inlay-hints", s.handleLSPInlayHints)
			r.Get("/file", s.handleLSPExternalFile)
		})
		r.Route("/debug", func(r chi.Router) {
			r.Post("/targets", s.handleDebugTargets)
			r.Post("/plan", s.handleDebugPlan)
			r.Post("/start", s.handleDebugStart)
			r.Get("/session", s.handleDebugSession)
			r.Get("/state", s.handleDebugState)
			r.Get("/inspection", s.handleDebugInspection)
			r.Put("/breakpoints", s.handleDebugBreakpoints)
			r.Post("/control", s.handleDebugControl)
			r.Post("/evaluate", s.handleDebugEvaluate)
			r.Post("/scopes", s.handleDebugScopes)
			r.Post("/variables", s.handleDebugVariables)
		})
		r.Post("/editor/tab", s.handleEditorTab)
		r.Post("/editor/transform", s.handleEditorTransform)
		r.Post("/settings/editor.tab.completion", s.handleEditorTabSettings)
		r.Post("/settings/window.terminal.position", s.handleWindowTerminalSettings)
		r.Get("/skills", s.handleSkills)
		r.Get("/capabilities", s.handleCapabilities)
	})

	fileServer := http.FileServer(http.FS(StaticFS))
	r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if p := strings.Trim(path.Clean(req.URL.Path), "/"); p != "" {
			if _, err := fs.Stat(StaticFS, p); err != nil {
				req.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, req)
	}))
}

func (s *Server) broadcast(f Frame) {
	f.Session = ""
	s.send(f)
}

const (
	wsWriteTimeout = 5 * time.Second
	wsOutboxBuffer = 256
)

type wsClient struct {
	conn   *websocket.Conn
	outbox chan []byte

	mu     sync.Mutex
	closed bool
}

func newWSClient(conn *websocket.Conn) *wsClient {
	return &wsClient{
		conn:   conn,
		outbox: make(chan []byte, wsOutboxBuffer),
	}
}

func (c *wsClient) enqueue(data []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.outbox <- data:
		return true
	default:
		return false
	}
}

func (c *wsClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.outbox)
}

func (c *wsClient) run() {
	for data := range c.outbox {
		ctx, cancel := context.WithTimeout(context.Background(), wsWriteTimeout)
		err := c.conn.Write(ctx, websocket.MessageText, data)
		cancel()
		if err != nil {
			_ = c.conn.CloseNow()

			for range c.outbox {
			}
			return
		}
	}
}

func (s *Server) send(f Frame) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	s.wsMu.Lock()
	clients := make([]*wsClient, 0, len(s.wsConns))
	for _, c := range s.wsConns {
		clients = append(clients, c)
	}
	s.wsMu.Unlock()
	for _, c := range clients {
		if !c.enqueue(data) {
			_ = c.conn.CloseNow()
		}
	}
}

type capabilitiesResponse struct {
	Git                    bool                            `json:"git"`
	GitInit                bool                            `json:"git_init"`
	LSP                    bool                            `json:"lsp"`
	Debug                  bool                            `json:"debug"`
	Tasks                  bool                            `json:"tasks"`
	Terminal               bool                            `json:"terminal"`
	Tab                    bool                            `json:"tab"`
	EditorTab              bool                            `json:"editor.tab.completion"`
	WindowTerminalPosition settings.WindowTerminalPosition `json:"window.terminal.position"`
	Platform               string                          `json:"platform"`
	WorkspaceName          string                          `json:"workspace_name"`
	ManagedTools           *managedToolsStatus             `json:"managed_tools,omitempty"`
}

type managedToolsStatus struct {
	State       string                 `json:"state"`
	Tool        string                 `json:"tool,omitempty"`
	Label       string                 `json:"label,omitempty"`
	Phase       devtools.ProgressPhase `json:"phase,omitempty"`
	Current     int                    `json:"current,omitempty"`
	Total       int                    `json:"total,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Unavailable []string               `json:"unavailable,omitempty"`
}

func (s *Server) setManagedToolsStatus(status managedToolsStatus) {
	s.managedToolsMu.Lock()
	s.managedTools = status
	s.managedToolsMu.Unlock()
	s.broadcast(Frame{Type: EvtCapabilitiesChanged})
}

func (s *Server) managedToolsStatus() managedToolsStatus {
	s.managedToolsMu.RLock()
	defer s.managedToolsMu.RUnlock()
	return s.managedTools
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	ws := s.workspace
	hasChanges := ws.HasChanges()
	caps := capabilitiesResponse{
		Git:                    hasChanges,
		GitInit:                !hasChanges,
		LSP:                    ws.HasLSP(),
		Debug:                  s.debugAvailable(r.Context()),
		Tasks:                  true,
		Terminal:               terminal.Supported(),
		Tab:                    s.tab != nil,
		EditorTab:              s.tab != nil && s.tabEnabled.Load(),
		WindowTerminalPosition: s.windowTerminalPosition(),
		Platform:               runtime.GOOS,
		WorkspaceName:          filepath.Base(ws.RootPath),
	}
	if status := s.managedToolsStatus(); status.State != "" {
		caps.ManagedTools = &status
	}
	writeJSON(w, caps)
}

func (s *Server) windowTerminalPosition() settings.WindowTerminalPosition {
	position, ok := s.terminalPosition.Load().(settings.WindowTerminalPosition)
	if !ok || !position.Valid() {
		return settings.WindowTerminalPositionTab
	}
	return position
}

func (s *Server) debugAvailable(ctx context.Context) bool {
	available := false
	_ = s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		if session := manager.ActiveSession(); session != nil && session.Status().State != dap.StateTerminated {
			available = true
			return nil
		}
		adapters, err := manager.Adapters(ctx)
		available = err == nil && len(adapters) > 0
		return nil
	})
	return available
}

func (s *Server) hasClients() bool {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return len(s.wsConns) > 0
}

func (s *Server) flushFiles() {
	if s.files != nil {
		s.files.Flush()
	}
	s.broadcast(Frame{Type: EvtFilesChanged})
}

func (s *Server) checkWorkspace() {
	ws := s.workspace

	gitNow := ws.IsGitRepo()
	if gitNow != s.prevGit {
		ws.SyncProjectMode()
		s.broadcast(Frame{Type: EvtCapabilitiesChanged})
		if ws.HasLSP() {
			s.broadcast(Frame{Type: EvtDiagnosticsChanged})
		}
		s.prevGit = gitNow
	}

	lspNow := ws.HasLSP()
	if lspNow != s.prevLSP {
		s.prevLSP = lspNow
		s.broadcast(Frame{Type: EvtCapabilitiesChanged})
		if lspNow {
			s.broadcast(Frame{Type: EvtDiagnosticsChanged})
		}
	}

	debugNow := s.debugAvailable(s.ctx)
	if debugNow != s.prevDebug {
		s.prevDebug = debugNow
		s.broadcast(Frame{Type: EvtCapabilitiesChanged})
	}

	if !ws.HasChanges() {
		s.broadcast(Frame{Type: EvtFilesChanged})
		if ws.HasLSP() {
			s.broadcast(Frame{Type: EvtDiagnosticsChanged})
		}
		return
	}
	fp := ws.ChangesFingerprint(s.ctx)
	if fp != s.prevFingerprint {
		s.prevFingerprint = fp
		s.broadcast(Frame{Type: EvtFilesChanged})
		s.broadcast(Frame{Type: EvtDiffsChanged})
		if ws.HasLSP() {
			s.broadcast(Frame{Type: EvtDiagnosticsChanged})
		}
	}
}

func (s *Server) refreshSkills() {
	if s.workspace.RefreshSkills() {
		s.broadcast(Frame{Type: EvtSkillsChanged})
	}
}

func convertMessages(messages []agent.Message) []ConversationMessage {
	var result []ConversationMessage
	for _, m := range messages {
		if m.Hidden {
			continue
		}
		cm := ConversationMessage{Role: string(m.Role), InputID: m.InputID}
		for _, c := range m.Content {
			if c.Hidden {
				continue
			}
			cc := ConversationContent{}
			if c.Text != "" {
				cc.Text = c.Text
				cc.TextID = c.TextID
			}
			if c.File != nil && c.File.Data != "" {
				cc.Image = &ConversationImage{Data: c.File.Data, Name: c.File.Name}
			}
			if c.Reasoning != nil && c.Reasoning.Summary != "" {
				cc.Reasoning = &ConversationReasoning{ID: c.Reasoning.ID, Summary: c.Reasoning.Summary}
			}
			if c.ToolCall != nil {
				display := displayTool(
					c.ToolCall.Name, c.ToolCall.Kind, c.ToolCall.Args, c.ToolCall.Locations,
					c.ToolCall.Presentation,
				)
				cc.ToolCall = &ConversationTool{
					ID:        c.ToolCall.ID,
					Name:      display.name,
					Kind:      display.kind,
					Args:      display.args,
					Locations: display.locations,
					Hint:      display.hint,
				}
			}
			if c.ToolResult != nil {
				display := displayTool(
					c.ToolResult.Name, c.ToolResult.Kind, c.ToolResult.Args, c.ToolResult.Locations,
					c.ToolResult.Presentation,
				)
				cc.ToolResult = &ConversationResult{
					ID:        c.ToolResult.ID,
					Name:      display.name,
					Kind:      display.kind,
					Args:      display.args,
					Locations: display.locations,
					Content:   c.ToolResult.Content,
				}
			}
			cm.Content = append(cm.Content, cc)
		}
		result = append(result, cm)
	}
	return result
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func resolvePort(port int) (int, error) {
	if port != 0 {
		return port, nil
	}
	return system.FreePort(DefaultPort)
}
