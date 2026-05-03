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
	"time"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/session"
	"github.com/adrianliechti/wingman-agent/pkg/system"

	"github.com/coder/websocket"
)

//go:generate npm --prefix ui install
//go:generate npm --prefix ui run build

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	agent     *code.Agent
	port      int
	sessionID string

	sessionsDir string

	// planMode toggles between the default agent system prompt and the
	// planning-only prompt; protected by mu.
	mu       sync.Mutex
	planMode bool

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

		askCh:    make(chan string, 1),
		promptCh: make(chan bool, 1),
	}
}

func newSessionID() string {
	return uuid.New().String()
}

func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Workspace probe + Rewind/LSP setup. Up to 4s on a non-git directory
	// that's too large; the browser opens after this completes so /api/
	// capabilities returns the correct state on first fetch.
	s.agent.WarmUp()

	// Init MCP
	if err := s.agent.InitMCP(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "MCP init warning: %v\n", err)
	}

	// Wire the system prompt builder. Mirrors the CLI's setup in app/app.go:115
	// — the agent invokes this lazily on every Send, so toggling plan mode
	// takes effect on the next turn.
	s.agent.Config.Instructions = s.currentInstructions

	// Poll for changes from outside the agent (terminal `rm`, IDE saves, etc.)
	// so the FileTree and Diffs panels reflect them. Polling instead of
	// fsnotify: zero FDs (kqueue's per-dir watcher cost was the original
	// $HOME crash), one path everywhere, ≤2s latency — fine for this UI.
	go s.pollFiles(ctx)

	// Auto-select model
	s.autoSelectModel(ctx)

	port, err := system.FreePort(s.port)
	if err != nil {
		return err
	}
	s.port = port

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	server := &http.Server{
		Addr:    fmt.Sprintf("localhost:%d", s.port),
		Handler: mux,
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		cancel()
		server.Close()
	}()

	url := fmt.Sprintf("http://localhost:%d", s.port)
	fmt.Fprintf(os.Stderr, "Wingman running at %s\n", url)

	// Open the URL in the user's default browser unless explicitly disabled
	// (CI, headless servers, SSH sessions, …).
	if os.Getenv("WINGMAN_NO_BROWSER") == "" {
		openBrowser(url)
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// API routes
	mux.HandleFunc("GET /api/files", s.handleFiles)
	mux.HandleFunc("GET /api/files/read", s.handleFileRead)
	mux.HandleFunc("GET /api/files/search", s.handleFilesSearch)
	mux.HandleFunc("GET /api/diffs", s.handleDiffs)
	mux.HandleFunc("GET /api/checkpoints", s.handleCheckpoints)
	mux.HandleFunc("POST /api/checkpoints/{hash}/restore", s.handleCheckpointRestore)
	mux.HandleFunc("GET /api/messages", s.handleMessages)
	mux.HandleFunc("GET /api/usage", s.handleUsage)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("POST /api/sessions/new", s.handleNewSession)
	mux.HandleFunc("POST /api/sessions/{id}/load", s.handleLoadSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/model", s.handleModel)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("POST /api/model", s.handleSetModel)
	mux.HandleFunc("GET /api/mode", s.handleMode)
	mux.HandleFunc("POST /api/mode", s.handleSetMode)
	mux.HandleFunc("GET /api/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("GET /api/skills", s.handleSkills)
	mux.HandleFunc("GET /api/capabilities", s.handleCapabilities)

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
		"cached_tokens": usage.CachedTokens,
		"output_tokens": usage.OutputTokens,
	})
}

// sendMessage marshals an event with its type field injected and writes it
// to the active WebSocket. Field order in the resulting JSON is unspecified —
// JSON consumers don't depend on it.
func (s *Server) sendMessage(e ServerEvent) {
	s.wsMu.Lock()
	conn := s.wsConn
	s.wsMu.Unlock()

	if conn == nil {
		return
	}

	payload, err := json.Marshal(e)
	if err != nil {
		return
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return
	}

	typeJSON, err := json.Marshal(e.serverEventType())
	if err != nil {
		return
	}
	fields["type"] = typeJSON

	data, err := json.Marshal(fields)
	if err != nil {
		return
	}

	conn.Write(context.Background(), websocket.MessageText, data)
}

func (s *Server) askUser(ctx context.Context, question string) (string, error) {
	s.sendMessage(AskEvent{Question: question})

	// Drain any stale response
	select {
	case <-s.askCh:
	default:
	}

	return <-s.askCh, nil
}

func (s *Server) promptUser(ctx context.Context, prompt string) (bool, error) {
	s.sendMessage(PromptEvent{Question: prompt})

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
		if m.Hidden {
			continue
		}

		cm := ConversationMessage{
			Role: string(m.Role),
		}

		for _, c := range m.Content {
			cc := ConversationContent{}

			if c.Text != "" {
				cc.Text = c.Text
			}

			if c.Reasoning != nil && c.Reasoning.Summary != "" {
				cc.Reasoning = &ConversationReasoning{
					ID:      c.Reasoning.ID,
					Summary: c.Reasoning.Summary,
				}
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
		writeJSON(w, []SessionEntry{})
		return
	}

	result := make([]SessionEntry, 0, len(sessions))
	for _, sess := range sessions {
		result = append(result, SessionEntry{
			ID:        sess.ID,
			Title:     sess.Title,
			CreatedAt: sess.CreatedAt.Format("2006-01-02 15:04"),
			UpdatedAt: sess.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}

	writeJSON(w, result)
}

func (s *Server) handleNewSession(w http.ResponseWriter, r *http.Request) {
	s.agent.Messages = nil
	s.agent.Usage = agent.Usage{}
	s.sessionID = newSessionID()

	// Re-baseline rewind for the new session and nudge every right-panel
	// listing so it replaces stale state. capabilities_changed covers the
	// case where the user ran `git init` between sessions.
	s.agent.RestartRewind()
	s.sendMessage(CapabilitiesChangedEvent{})
	s.sendMessage(DiffsChangedEvent{})
	s.sendMessage(CheckpointsChangedEvent{})
	s.sendMessage(FilesChangedEvent{})
	s.sendMessage(SessionsChangedEvent{})

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

	if err := session.Delete(s.sessionsDir, id); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.sendMessage(SessionsChangedEvent{})

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
		writeJSON(w, []map[string]string{})
		return
	}

	// Index upstream models by ID so we can confirm each curated entry is
	// actually offered before exposing it.
	upstream := make(map[string]bool, len(models))
	for _, m := range models {
		upstream[m.ID] = true
	}

	// Preserve the curated order from code.AvailableModels — that's the order
	// the user expects to see in the picker.
	result := make([]map[string]string, 0, len(code.AvailableModels))
	for _, m := range code.AvailableModels {
		if !upstream[m.ID] {
			continue
		}
		result = append(result, map[string]string{
			"id":   m.ID,
			"name": m.Name,
		})
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

// pollFiles watches the working dir for external changes (terminal `rm`,
// IDE saves, etc.) so the FileTree and Diffs panels stay current. Each tick:
//
//  1. If no UI is connected, skip — the walk isn't free.
//  2. If the git status flipped (`git init` / `rm -rf .git`), rebuild LSP and
//     broadcast capabilities_changed so the right-panel tabs adjust.
//  3. Compute a worktree fingerprint and emit FilesChanged + DiffsChanged
//     only when it moves. Avoids two full server walks per tick on quiet
//     repos (one for /api/files, one for /api/diffs' snapshotTree).
func (s *Server) pollFiles(ctx context.Context) {
	const interval = 2 * time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	prevGit := s.agent.IsGitRepo()
	var prevFingerprint uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.wsMu.Lock()
			hasClient := s.wsConn != nil
			s.wsMu.Unlock()
			if !hasClient {
				continue
			}

			gitNow := s.agent.IsGitRepo()
			if gitNow != prevGit {
				s.agent.SyncProjectMode()
				s.sendMessage(CapabilitiesChangedEvent{})
				if s.agent.LSP != nil {
					s.sendMessage(DiagnosticsChangedEvent{})
				}
				prevGit = gitNow
			}

			// On unsupported workspaces, skip the worktree fingerprint walk
			// (would chew through a huge dir) and just nudge the file tree.
			// The tree's per-dir fetches are cheap; expanded dirs refresh
			// without scanning everything.
			if s.agent.Rewind == nil {
				s.sendMessage(FilesChangedEvent{})
				continue
			}

			fp := s.agent.Rewind.Fingerprint()
			if fp != prevFingerprint {
				s.sendMessage(FilesChangedEvent{})
				s.sendMessage(DiffsChangedEvent{})
				prevFingerprint = fp
			}
		}
	}
}

// handleCapabilities reports which features the working directory supports.
// The web UI fetches this once on load to decide which tabs/panels to show.
// Rewind/LSP only run on "supported" workspaces (git repos or directories
// small enough to walk in WarmUp's budget); on an unsupported dir (e.g.
// $HOME) those backends never started and the UI surfaces `notice` as a
// banner so the user understands why the right-panel tabs are missing.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	caps := map[string]any{
		"git":   s.agent.IsGitRepo(),
		"lsp":   s.agent.LSP != nil,
		"diffs": s.agent.Rewind != nil,
	}
	if s.agent.Rewind == nil {
		caps["notice"] = "This directory is too large for full features. Diffs, checkpoints, and code intelligence are disabled — chat and file browsing still work."
	}
	writeJSON(w, caps)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
