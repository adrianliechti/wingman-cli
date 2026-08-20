package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/go-shell"

	"github.com/adrianliechti/wingman-agent/pkg/settings"
	"github.com/adrianliechti/wingman-agent/server"
)

//go:embed all:public
var publicFS embed.FS

type App struct {
	mu     sync.Mutex
	server *server.Server

	launcher http.Handler
}

func main() {
	// Repair PATH before anything detects agents via exec.LookPath: GUI
	// launches (Finder/Dock) inherit a minimal PATH that hides Homebrew /
	// ~/.local/bin CLIs like codex and copilot.
	ensureShellPath()

	app := &App{}
	app.launcher = app.newLauncher()

	err := shell.Run(shell.Options{
		Title: "Wingman Agent",

		Handler:    app,
		OnShutdown: app.shutdown,

		Width:  1280,
		Height: 768,

		MinWidth:  640,
		MinHeight: 400,

		TitleBar: shell.TitleBarOptions{
			Overlay:         true,
			ControlsOffsetX: 4,
			ControlsOffsetY: 4,
		},
		FileMenu: []shell.MenuItem{
			{Title: "New File...", Command: "new-file", Key: "n", Disabled: true},
			{Title: "Open Folder...", Command: "open-folder", Key: "o"},
			{Separator: true},
			{Title: "Save", Command: "save", Key: "s", Disabled: true},
			{Title: "Save As...", Command: "save-as", Key: "s", Shift: true, Disabled: true},
			{Separator: true},
		},

		Debug: os.Getenv("WINGMAN_DEBUG") != "",
	})

	if err != nil {
		log.Fatal(err)
	}
}

// ServeHTTP hands everything to the workspace server once one is open;
// until then the launcher (start page + its API) answers. Both share the
// window's origin — go-shell opens any other origin in the default browser,
// and staying behind its session cookie keeps the workspace server
// unreachable for other local processes.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// App-level commands stay available after a workspace is mounted so the
	// native menu can select and switch folders without restarting the shell.
	if strings.HasPrefix(r.URL.Path, "/app/") {
		a.launcher.ServeHTTP(w, r)
		return
	}

	a.mu.Lock()
	srv := a.server
	a.mu.Unlock()

	if srv != nil {
		srv.ServeHTTP(w, r)
		return
	}

	a.launcher.ServeHTTP(w, r)
}

func (a *App) newLauncher() http.Handler {
	public, _ := fs.Sub(publicFS, "public")

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(public)))

	mux.HandleFunc("GET /app/workspaces", a.handleWorkspaces)
	mux.HandleFunc("POST /app/workspaces/remove", a.handleRemoveWorkspace)
	mux.HandleFunc("POST /app/workspaces/open", a.handleOpenWorkspace)
	mux.HandleFunc("POST /app/folder", a.handleSelectFolder)

	return mux
}

func (a *App) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	s, err := settings.Load()

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	workspaces := s.Workspaces

	if len(workspaces) > settings.MaxWorkspaces {
		workspaces = workspaces[:settings.MaxWorkspaces]
	}

	writeJSON(w, workspaces)
}

func (a *App) handleRemoveWorkspace(w http.ResponseWriter, r *http.Request) {
	path, ok := readPath(w, r)

	if !ok {
		return
	}

	s, err := settings.Update(func(current *settings.Settings) {
		current.RemoveWorkspace(path)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, s.Workspaces)
}

func (a *App) handleOpenWorkspace(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path    string `json:"path"`
		Replace bool   `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if request.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	current := a.server
	if current != nil && !request.Replace {
		a.mu.Unlock()
		http.Error(w, "workspace already open", http.StatusConflict)
		return
	}
	a.mu.Unlock()

	srv, err := server.New(context.Background(), request.Path, nil)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.mu.Lock()
	if a.server != current {
		a.mu.Unlock()
		srv.Close()
		http.Error(w, "workspace changed while opening", http.StatusConflict)
		return
	}
	a.server = srv
	a.mu.Unlock()

	_, _ = settings.Update(func(settings *settings.Settings) {
		settings.AddWorkspace(request.Path)
	})

	w.WriteHeader(http.StatusNoContent)

	if current != nil {
		go current.Close()
	}
}

func (a *App) handleSelectFolder(w http.ResponseWriter, r *http.Request) {
	path, err := shell.PickFolder("Open Workspace")

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"path": path})
}

// shutdown bounds the teardown so a slow component (LSP shutdown
// handshakes, MCP subprocesses) can't hang app quit. Kill signals are
// issued before the waits we abandon.
func (a *App) shutdown() {
	a.mu.Lock()
	srv := a.server
	a.mu.Unlock()

	if srv == nil {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Close()
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		log.Println("shutdown timed out, exiting anyway")
	}
}

func readPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Path string `json:"path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}

	if req.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return "", false
	}

	return req.Path, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
