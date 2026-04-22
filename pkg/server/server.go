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
	"path/filepath"
	"sort"
	"sync"
	"syscall"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/app/rewind"
	"github.com/adrianliechti/wingman-agent/app/session"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"

	"github.com/coder/websocket"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	agent     *code.Agent
	port      int
	sessionID string

	sessionsDir string

	rewind      *rewind.Manager
	rewindReady chan struct{}

	// WebSocket state (single client)
	wsMu         sync.Mutex
	wsConn       *websocket.Conn
	streamCancel context.CancelFunc

	// Channels for ask/prompt relay
	askCh    chan string
	promptCh chan bool
}

func New(agent *code.Agent, port int) *Server {
	sessionsDir := filepath.Join(filepath.Dir(agent.MemoryPath), "sessions")

	return &Server{
		agent:     agent,
		port:      port,
		sessionID: newSessionID(),

		sessionsDir: sessionsDir,

		rewindReady: make(chan struct{}),
		askCh:       make(chan string, 1),
		promptCh:    make(chan bool, 1),
	}
}

func newSessionID() string {
	return uuid.New().String()
}

func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Init MCP
	if err := s.agent.InitMCP(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "MCP init warning: %v\n", err)
	}

	// Init rewind async
	go func() {
		defer close(s.rewindReady)

		workDir := s.agent.RootPath
		gitDir := filepath.Join(workDir, ".git")

		if _, err := os.Stat(gitDir); os.IsNotExist(err) {
			return
		}

		if rm, err := rewind.New(workDir); err == nil {
			s.rewind = rm
		}
	}()

	// Auto-select model
	s.autoSelectModel(ctx)

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nShutting down...")
		cancel()
		server.Close()
	}()

	fmt.Fprintf(os.Stderr, "Wingman server running at http://localhost:%d\n", s.port)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	// Cleanup
	select {
	case <-s.rewindReady:
		if s.rewind != nil {
			s.rewind.Cleanup()
		}
	default:
	}

	return nil
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// API routes
	mux.HandleFunc("GET /api/files", s.handleFiles)
	mux.HandleFunc("GET /api/files/read", s.handleFileRead)
	mux.HandleFunc("GET /api/diffs", s.handleDiffs)
	mux.HandleFunc("GET /api/messages", s.handleMessages)
	mux.HandleFunc("GET /api/usage", s.handleUsage)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("POST /api/sessions/new", s.handleNewSession)
	mux.HandleFunc("POST /api/sessions/{id}/load", s.handleLoadSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/model", s.handleModel)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("POST /api/model", s.handleSetModel)
	mux.HandleFunc("GET /api/diagnostics", s.handleDiagnostics)

	// WebSocket
	mux.HandleFunc("/ws/chat", s.handleWebSocket)

	// Static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("/", fileServer)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	messages := convertMessages(s.agent.Messages)
	writeJSON(w, messages)
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	usage := s.agent.Usage
	writeJSON(w, map[string]int64{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	})
}

func (s *Server) sendMessage(msg ServerMessage) {
	s.wsMu.Lock()
	conn := s.wsConn
	s.wsMu.Unlock()

	if conn == nil {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	conn.Write(context.Background(), websocket.MessageText, data)
}

func (s *Server) askUser(ctx context.Context, question string) (string, error) {
	s.sendMessage(ServerMessage{Type: MsgAsk, Question: question})

	// Drain any stale response
	select {
	case <-s.askCh:
	default:
	}

	return <-s.askCh, nil
}

func (s *Server) promptUser(ctx context.Context, prompt string) (bool, error) {
	s.sendMessage(ServerMessage{Type: MsgPrompt, Question: prompt})

	// Drain any stale response
	select {
	case <-s.promptCh:
	default:
	}

	return <-s.promptCh, nil
}

func convertMessages(messages []agent.Message) []ConversationMessage {
	var result []ConversationMessage

	for _, m := range messages {
		cm := ConversationMessage{
			Role: string(m.Role),
		}

		for _, c := range m.Content {
			cc := ConversationContent{}

			if c.Text != "" {
				cc.Text = c.Text
			}

			if c.ToolCall != nil {
				cc.ToolCall = &ConversationTool{
					ID:   c.ToolCall.ID,
					Name: c.ToolCall.Name,
					Args: c.ToolCall.Args,
				}
			}

			if c.ToolResult != nil {
				cc.ToolResult = &ConversationResult{
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

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := session.List(s.sessionsDir)
	if err != nil {
		writeJSON(w, []any{})
		return
	}

	type sessionInfo struct {
		ID        string `json:"id"`
		Title     string `json:"title,omitempty"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}

	var result []sessionInfo
	for _, sess := range sessions {
		result = append(result, sessionInfo{
			ID:        sess.ID,
			Title:     sess.Title,
			CreatedAt: sess.CreatedAt.Format("2006-01-02 15:04"),
			UpdatedAt: sess.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}

	if result == nil {
		result = []sessionInfo{}
	}

	writeJSON(w, result)
}

func (s *Server) handleNewSession(w http.ResponseWriter, r *http.Request) {
	s.agent.Messages = nil
	s.agent.Usage = agent.Usage{}

	// Reset rewind
	select {
	case <-s.rewindReady:
		if s.rewind != nil {
			s.rewind.Cleanup()
		}
	default:
	}

	// Re-init rewind
	s.rewindReady = make(chan struct{})
	go func() {
		defer close(s.rewindReady)
		workDir := s.agent.RootPath
		gitDir := filepath.Join(workDir, ".git")
		if _, err := os.Stat(gitDir); os.IsNotExist(err) {
			return
		}
		if rm, err := rewind.New(workDir); err == nil {
			s.rewind = rm
		}
	}()

	s.sessionID = newSessionID()

	writeJSON(w, map[string]string{"id": s.sessionID})
}

func (s *Server) handleLoadSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}

	sess, err := session.Load(s.sessionsDir, id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	s.agent.Messages = sess.State.Messages
	s.agent.Usage = sess.State.Usage
	s.sessionID = id

	messages := convertMessages(s.agent.Messages)
	writeJSON(w, messages)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}

	session.Delete(s.sessionsDir, id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	model := ""
	if s.agent.Config.Model != nil {
		model = s.agent.Model()
	}
	writeJSON(w, map[string]string{"model": model})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.agent.Models(r.Context())
	if err != nil {
		writeJSON(w, []any{})
		return
	}

	var result []map[string]string
	for _, m := range models {
		result = append(result, map[string]string{"id": m.ID})
	}

	if result == nil {
		result = []map[string]string{}
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

	modelID := body.Model
	s.agent.Config.Model = func() string { return modelID }

	writeJSON(w, map[string]string{"model": modelID})
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if s.agent.LSP == nil {
		writeJSON(w, []any{})
		return
	}

	allDiags := s.agent.LSP.CollectAllDiagnostics(r.Context())

	type diagItem struct {
		Path     string `json:"path"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Source   string `json:"source,omitempty"`
	}

	var result []diagItem

	for filePath, diags := range allDiags {
		// Make path relative to workspace
		relPath := filePath
		if rel, err := filepath.Rel(s.agent.RootPath, filePath); err == nil {
			relPath = rel
		}

		for _, d := range diags {
			sev := "info"
			switch d.Severity {
			case lsp.DiagnosticSeverityError:
				sev = "error"
			case lsp.DiagnosticSeverityWarning:
				sev = "warning"
			}

			result = append(result, diagItem{
				Path:     relPath,
				Line:     d.Range.Start.Line + 1, // 0-based to 1-based
				Column:   d.Range.Start.Character + 1,
				Severity: sev,
				Message:  d.Message,
				Source:   d.Source,
			})
		}
	}

	if result == nil {
		result = []diagItem{}
	}

	// Sort: errors first, then warnings, then info; within same severity by path then line
	sevOrder := map[string]int{"error": 0, "warning": 1, "info": 2}
	sort.Slice(result, func(i, j int) bool {
		si, sj := sevOrder[result[i].Severity], sevOrder[result[j].Severity]
		if si != sj {
			return si < sj
		}
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Line < result[j].Line
	})

	writeJSON(w, result)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
