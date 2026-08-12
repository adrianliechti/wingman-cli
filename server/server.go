package server

import (
	"cmp"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code/agents"
	"github.com/adrianliechti/wingman-agent/pkg/system"
	"github.com/adrianliechti/wingman-agent/pkg/terminal"
	"github.com/adrianliechti/wingman-agent/pkg/watch"
)

var _ code.UI = (*Server)(nil)

//go:embed static/*
var staticFiles embed.FS

var StaticFS, _ = fs.Sub(staticFiles, "static")

// DefaultPort is the preferred port used when no explicit port is requested.
const DefaultPort = 9000

type ServerOptions struct {
	NoBrowser bool
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

	mu    sync.Mutex
	agent code.Agent
	turns *code.TurnManager

	turnMetaMu sync.Mutex
	turnMeta   map[string]map[string]ClientMessage
	turnUsage  map[string]agent.Usage

	phasesMu sync.Mutex
	phases   map[string]string

	wsMu    sync.Mutex
	wsConns map[*websocket.Conn]*wsClient
	sendMu  sync.Mutex

	promptsMu      sync.Mutex
	pendingPrompts map[string]pendingPrompt
	confirmAll     map[string]bool

	lspExternalMu    sync.Mutex
	lspExternalPaths map[string]bool

	taskPumpMu sync.Mutex
	taskPumps  map[*task.Registry]bool

	terminals *terminal.Manager
	preview   *filePreviewServer

	files           *watch.Monitor
	prevGit         bool
	prevLSP         bool
	prevFingerprint uint64
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
	ws, err := code.NewWorkspace(workDir)
	if err != nil {
		return nil, err
	}
	serverCtx, cancel := context.WithCancel(ctx)

	s := &Server{
		noBrowser:      opts.NoBrowser,
		workspace:      ws,
		config:         cfg,
		ctx:            serverCtx,
		cancel:         cancel,
		phases:         map[string]string{},
		wsConns:        map[*websocket.Conn]*wsClient{},
		pendingPrompts: map[string]pendingPrompt{},
		confirmAll:     map[string]bool{},
		turnMeta:       map[string]map[string]ClientMessage{},
		turnUsage:      map[string]agent.Usage{},
	}

	s.terminals = terminal.NewManager(ws.RootPath)
	s.terminals.SetExitHandler(s.onTerminalExit)
	s.preview, err = newFilePreviewServer(ws.Root)
	if err != nil {
		cancel()
		ws.Close()
		return nil, err
	}

	wa := codeagent.New(ws, cfg, nil)
	wa.SetUI(s)
	s.agent = wa
	s.turns = code.NewTurnManager(tool.WithProgressSink(serverCtx, s.onToolProgress), wa, s.handleTurnEvent)

	ws.WarmUp()

	s.prevGit = ws.IsGitRepo()
	s.prevLSP = ws.HasLSP()
	s.files = watch.New(watch.Options{Active: s.hasClients}, s.checkWorkspace)
	s.background.Go(func() {
		s.files.Run(serverCtx)
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
		s.broadcast(Frame{Type: EvtModelChanged})
	})

	s.mux = chi.NewRouter()
	s.registerRoutes(s.mux)

	csrf := http.NewCrossOriginProtection()
	s.handler = csrf.Handler(s.mux)

	return s, nil
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}

		s.mu.Lock()
		a := s.agent
		turns := s.turns
		s.agent = nil
		s.turns = nil
		s.mu.Unlock()
		if turns != nil {
			turns.SetHandler(nil)
			turns.Close()
		}
		if a != nil {
			_ = a.Close()
		}
		s.background.Wait()
		s.preview.Close()
		s.terminals.Close()
		s.workspace.Close()
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) activeAgent() code.Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agent
}

func (s *Server) swapAgent(next code.Agent) {
	s.mu.Lock()
	prev := s.agent
	prevTurns := s.turns
	s.agent = next
	s.turns = code.NewTurnManager(tool.WithProgressSink(s.ctx, s.onToolProgress), next, s.handleTurnEvent)
	s.mu.Unlock()
	if prevTurns != nil {
		prevTurns.SetHandler(nil)
		prevTurns.Close()
	}
	s.turnMetaMu.Lock()
	s.turnMeta = map[string]map[string]ClientMessage{}
	s.turnUsage = map[string]agent.Usage{}
	s.turnMetaMu.Unlock()
	s.promptsMu.Lock()
	s.confirmAll = map[string]bool{}
	s.promptsMu.Unlock()
	s.phasesMu.Lock()
	s.phases = map[string]string{}
	s.phasesMu.Unlock()
	if prev != nil && prev != next {
		_ = prev.Close()
	}
}

func (s *Server) onToolProgress(ctx context.Context, callID, text string) {
	if sid := code.SessionIDFromContext(ctx); sid != "" {
		s.sendSession(sid, Frame{Type: EvtToolProgress, ID: callID, Text: text})
	}
}

func (s *Server) activeRuntime() (code.Agent, *code.TurnManager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agent, s.turns
}

func (s *Server) handleWebSocketURL(w http.ResponseWriter, r *http.Request) {
	proto := "ws"
	if r.TLS != nil {
		proto = "wss"
	}
	writeJSON(w, map[string]string{"url": fmt.Sprintf("%s://%s/ws", proto, r.Host)})
}

func (s *Server) Run(ctx context.Context, port int) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.ctx = ctx

	resolvedPort, err := resolvePort(port)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf("localhost:%d", resolvedPort),
		Handler: s,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		srv.Close()
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
			r.Delete("/", s.handleFileDelete)
			r.Get("/read", s.handleFileRead)
			r.Get("/search", s.handleFilesSearch)
			r.Get("/download", s.handleFileDownload)
			r.Get("/preview", s.handleFilePreview)
			r.Post("/rename", s.handleFileRename)
			r.Post("/copy", s.handleFileCopy)
			r.Post("/write", s.handleFileWrite)
		})

		r.Route("/diffs", func(r chi.Router) {
			r.Get("/", s.handleDiffs)
			r.Post("/revert", s.handleDiffRevert)
		})

		r.Route("/git", func(r chi.Router) {
			r.Post("/init", s.handleGitInit)
			r.Get("/status", s.handleGitStatus)
			r.Get("/branches", s.handleGitBranches)
			r.Post("/branches", s.handleGitCreateBranch)
			r.Post("/checkout", s.handleGitCheckoutBranch)
			r.Post("/stage", s.handleGitStage)
			r.Post("/unstage", s.handleGitUnstage)
			r.Post("/commit", s.handleGitCommit)
			r.Post("/pull", s.handleGitPull)
			r.Post("/push", s.handleGitPush)
		})

		r.Route("/sessions", func(r chi.Router) {
			r.Get("/", s.handleSessions)
			r.Post("/", s.handleNewSession)
			r.Route("/{id}", func(r chi.Router) {
				r.Delete("/", s.handleDeleteSession)
				r.Post("/load", s.handleLoadSession)
				r.Get("/model", s.handleModel)
				r.Post("/model", s.handleSetModel)
				r.Get("/effort", s.handleEffort)
				r.Post("/effort", s.handleSetEffort)
				r.Get("/mode", s.handleMode)
				r.Post("/mode", s.handleSetMode)
				r.Get("/tasks", s.handleTasks)
				r.Get("/tasks/{taskID}", s.handleTask)
				r.Post("/tasks/{taskID}/stop", s.handleTaskStop)
				r.Get("/schedules", s.handleSchedules)
				r.Delete("/schedules/{scheduleID}", s.handleScheduleDelete)
				r.Post("/schedules/{scheduleID}/pause", s.handleSchedulePause)
				r.Post("/schedules/{scheduleID}/resume", s.handleScheduleResume)
			})
		})

		r.Get("/models", s.handleModels)
		r.Get("/model", s.handleModel)
		r.Post("/model", s.handleSetModel)
		r.Get("/effort", s.handleEffort)
		r.Post("/effort", s.handleSetEffort)
		r.Get("/mode", s.handleMode)

		r.Get("/agents", s.handleAgents)
		r.Get("/agent", s.handleAgent)
		r.Post("/agent", s.handleSetAgent)

		r.Route("/terminals", func(r chi.Router) {
			r.Get("/", s.handleTerminals)
			r.Post("/", s.handleNewTerminal)
			r.Get("/shells", s.handleTerminalShells)
			r.Get("/{id}", s.handleTerminal)
			r.Delete("/{id}", s.handleDeleteTerminal)
			r.HandleFunc("/{id}/ws", s.handleTerminalWebSocket)
		})

		r.Route("/lsp", func(r chi.Router) {
			r.Get("/diagnostics", s.handleDiagnostics)
			r.Post("/diagnostics", s.handleLSPFileDiagnostics)
			r.Post("/definition", s.handleLSPDefinition)
			r.Post("/type-definition", s.handleLSPTypeDefinition)
			r.Post("/implementations", s.handleLSPImplementations)
			r.Post("/references", s.handleLSPReferences)
			r.Post("/hover", s.handleLSPHover)
			r.Post("/document-symbols", s.handleLSPDocumentSymbols)
			r.Get("/file", s.handleLSPExternalFile)
		})
		r.Get("/skills", s.handleSkills)
		r.Get("/capabilities", s.handleCapabilities)
		r.Get("/ws", s.handleWebSocketURL)
	})

	r.HandleFunc("/ws", s.handleWebSocket)

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

func (s *Server) sessionPhase(id string) string {
	s.phasesMu.Lock()
	defer s.phasesMu.Unlock()
	if p := s.phases[id]; p != "" {
		return p
	}
	return "idle"
}

func (s *Server) setSessionPhase(id, phase string) {
	s.phasesMu.Lock()
	if s.phases[id] == phase {
		s.phasesMu.Unlock()
		return
	}
	if phase == "" || phase == "idle" {
		delete(s.phases, id)
	} else {
		s.phases[id] = phase
	}
	s.phasesMu.Unlock()
	s.sendSession(id, Frame{Type: EvtPhase, Phase: phase})
}

func (s *Server) sendSession(sid string, f Frame) {
	f.Session = sid
	s.send(f)
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

func (s *Server) sendSessionState(sid string) {
	a := s.activeAgent()
	if a == nil {
		return
	}
	s.sendSessionSnapshot(sid, a.Messages(sid), a.Usage(sid))
	for _, f := range s.pendingPromptFramesFor(sid) {
		s.send(f)
	}
	s.sendTurnSnapshot(sid)
}

func (s *Server) sendSessionSnapshot(sid string, messages []agent.Message, u agent.Usage) {
	frame := Frame{
		Type:         EvtSessionState,
		Phase:        s.sessionPhase(sid),
		Messages:     convertMessages(messages),
		InputTokens:  u.InputTokens,
		CachedTokens: u.CachedTokens,
		OutputTokens: u.OutputTokens,

		LastInputTokens: u.LastInputTokens,
		ContextWindow:   u.ContextWindow,
	}
	if a := s.activeAgent(); a != nil && frame.ContextWindow <= 0 && u.LastInputTokens > 0 {
		_, model := a.Models(sid)
		frame.ContextWindow = int64(agent.ContextWindowFor(model, false))
	}
	s.sendSession(sid, frame)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	a := s.activeAgent()
	if a == nil {
		writeJSON(w, []SessionEntry{})
		return
	}
	infos, err := a.ListSessions(r.Context())
	if err != nil {
		fmt.Fprintf(os.Stderr, "list sessions (%s): %v\n", a.Name(), err)
		http.Error(w, fmt.Sprintf("list sessions: %v", err), http.StatusBadGateway)
		return
	}
	out := make([]SessionEntry, 0, len(infos))
	for _, si := range infos {
		ent := SessionEntry{ID: si.ID, Title: si.Title}
		if !si.UpdatedAt.IsZero() {
			ent.UpdatedAt = si.UpdatedAt.Format(time.RFC3339)
			ent.CreatedAt = ent.UpdatedAt
		}
		out = append(out, ent)
	}
	slices.SortFunc(out, func(a, b SessionEntry) int {
		return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
	})
	writeJSON(w, out)
}

func (s *Server) handleNewSession(w http.ResponseWriter, r *http.Request) {
	a := s.activeAgent()
	if a == nil {
		http.Error(w, "no active agent", http.StatusInternalServerError)
		return
	}
	id, err := a.NewSession(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.broadcast(Frame{Type: EvtModelChanged})
	s.sendTurnSnapshot(id)
	s.ensureTaskPump(id)
	writeJSON(w, map[string]string{"id": id})
}

func (s *Server) handleLoadSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}
	a := s.activeAgent()
	if a == nil {
		http.Error(w, "no active agent", http.StatusInternalServerError)
		return
	}

	loadCtx := code.WithSessionID(s.ctx, id)
	var err error
	if loader, ok := a.(code.SessionLoadStreamer); ok {
		err = s.streamLoad(loadCtx, loader, id)
	} else {
		err = a.LoadSession(loadCtx, id)
	}
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			http.Error(w, "load not supported for this agent", http.StatusMethodNotAllowed)
			return
		}
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.sendSessionState(id)
	s.ensureTaskPump(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) streamLoad(ctx context.Context, loader code.SessionLoadStreamer, id string) error {
	a := s.activeAgent()
	const minInterval = 150 * time.Millisecond
	var last time.Time
	for msgs, err := range loader.LoadSessionStream(ctx, id) {
		if err != nil {
			return err
		}
		now := time.Now()
		if !last.IsZero() && now.Sub(last) < minInterval {
			continue
		}
		last = now
		s.sendSessionSnapshot(id, msgs, a.Usage(id))
	}
	return nil
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}
	a := s.activeAgent()
	if a == nil {
		http.Error(w, "no active agent", http.StatusInternalServerError)
		return
	}
	if _, turns := s.activeRuntime(); turns != nil {
		turns.CancelAll(id)
	}
	if err := a.DeleteSession(r.Context(), id); err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			http.Error(w, "delete not supported for this agent", http.StatusMethodNotAllowed)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.phasesMu.Lock()
	delete(s.phases, id)
	s.phasesMu.Unlock()
	s.broadcast(Frame{Type: EvtSessionsChanged})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	a := s.activeAgent()
	if a == nil {
		writeJSON(w, map[string]string{"model": ""})
		return
	}
	_, current := a.Models(r.PathValue("id"))
	writeJSON(w, map[string]string{"model": current})
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	a := s.activeAgent()
	if a == nil {
		writeJSON(w, []map[string]string{})
		return
	}
	available, _ := a.Models("")
	result := make([]map[string]string, 0, len(available))
	for _, m := range available {
		result = append(result, map[string]string{"id": m.ID, "name": m.Name})
	}
	writeJSON(w, result)
}

func (s *Server) handleSetModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}
	a := s.activeAgent()
	if a == nil {
		http.Error(w, "no active agent", http.StatusInternalServerError)
		return
	}
	if err := a.SetModel(r.Context(), r.PathValue("id"), body.Model); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.broadcast(Frame{Type: EvtModelChanged})
	writeJSON(w, map[string]string{"model": body.Model})
}

func (s *Server) handleEffort(w http.ResponseWriter, r *http.Request) {
	a := s.activeAgent()
	if a == nil {
		writeJSON(w, map[string]any{"effort": "", "options": []string{}})
		return
	}
	current, options := a.Effort(r.PathValue("id"))
	writeJSON(w, map[string]any{"effort": current, "options": options})
}

func (s *Server) handleSetEffort(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Effort string `json:"effort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	a := s.activeAgent()
	if a == nil {
		http.Error(w, "no active agent", http.StatusInternalServerError)
		return
	}
	if err := a.SetEffort(r.Context(), r.PathValue("id"), body.Effort); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"effort": body.Effort})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	ws := s.workspace
	_, isCoder := s.activeAgent().(*codeagent.Agent)
	caps := map[string]any{
		"lsp":      ws.HasLSP(),
		"diffs":    ws.HasChanges(),
		"git":      ws.HasChanges(),
		"git_init": isCoder && !ws.HasChanges(),
		"tasks":    isCoder,
		"terminal": terminal.Supported(),
	}
	writeJSON(w, caps)
}

func (s *Server) hasClients() bool {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return len(s.wsConns) > 0
}

func (s *Server) flushFiles() {
	s.files.Flush()
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

func convertMessages(messages []agent.Message) []ConversationMessage {
	var result []ConversationMessage
	for _, m := range messages {
		if m.Hidden {
			continue
		}
		cm := ConversationMessage{Role: string(m.Role)}
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
				cc.ToolCall = &ConversationTool{
					ID:   c.ToolCall.ID,
					Name: c.ToolCall.Name,
					Args: c.ToolCall.Args,
					Hint: tool.ExtractHint(c.ToolCall.Args, c.ToolCall.Name),
				}
			}
			if c.ToolResult != nil {
				cc.ToolResult = &ConversationResult{
					ID:      c.ToolResult.ID,
					Name:    c.ToolResult.Name,
					Args:    c.ToolResult.Args,
					Content: c.ToolResult.Content,
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

func (s *Server) constructBackend(name string) (code.Agent, error) {
	a, err := agents.New(s.ctx, s.workspace, name, s.config)
	if err != nil {
		return nil, err
	}
	if us, ok := a.(interface{ SetUI(code.UI) }); ok {
		us.SetUI(s)
	}
	if w, ok := a.(*codeagent.Agent); ok {
		w.FetchModels(s.ctx)
	}
	return a, nil
}
