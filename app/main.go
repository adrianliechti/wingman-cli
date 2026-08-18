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

	shell "github.com/adrianliechti/go-shell"

	"github.com/adrianliechti/wingman-agent/server"
)

//go:embed all:public
var publicFS embed.FS

type App struct {
	mu     sync.Mutex
	server workspaceServer

	launcher http.Handler

	startRemote func(RemoteWorkspace, Settings, remoteCredentials) (workspaceServer, error)
}

func main() {
	// OpenSSH invokes this executable as its graphical password helper when an
	// SSH workspace was opened with a one-time credential. Handle that tiny
	// helper mode before initializing the desktop shell.
	if runSSHAskpass() {
		return
	}

	// Repair PATH before anything detects agents via exec.LookPath: GUI
	// launches (Finder/Dock) inherit a minimal PATH that hides Homebrew /
	// ~/.local/bin CLIs like codex and copilot.
	ensureShellPath()

	if s, err := loadSettings(); err == nil {
		s.Apply()
	}

	app := &App{}
	app.launcher = app.newLauncher()

	err := shell.Run(shell.Options{
		Title:   "Wingman Agent",
		Handler: app,

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

	app.shutdown()
}

// ServeHTTP hands everything to the workspace server once one is open;
// until then the launcher (start page + its API) answers. Both share the
// window's origin — go-shell opens any other origin in the default browser,
// and staying behind its session cookie keeps local and proxied remote
// workspace servers unreachable for other local processes.
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

	mux.HandleFunc("GET /app/settings", a.handleSettings)
	mux.HandleFunc("POST /app/settings", a.handleSaveSettings)
	mux.HandleFunc("GET /app/workspaces", a.handleWorkspaces)
	mux.HandleFunc("POST /app/workspaces/remove", a.handleRemoveWorkspace)
	mux.HandleFunc("POST /app/workspaces/open", a.handleOpenWorkspace)
	mux.HandleFunc("GET /app/remotes", a.handleRemotes)
	mux.HandleFunc("POST /app/remotes/save", a.handleSaveRemote)
	mux.HandleFunc("POST /app/remotes/remove", a.handleRemoveRemote)
	mux.HandleFunc("POST /app/remotes/open", a.handleOpenRemote)
	mux.HandleFunc("POST /app/folder", a.handleSelectFolder)

	return mux
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	s, err := loadSettings()

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, s)
}

func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var s Settings

	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if current, err := loadSettings(); err == nil {
		s.Workspaces = current.Workspaces
		s.Remotes = current.Remotes
	}

	if err := saveSettings(s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.Apply()

	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleRemotes(w http.ResponseWriter, _ *http.Request) {
	s, err := loadSettings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	remotes := s.Remotes
	if len(remotes) > maxRemoteWorkspaces {
		remotes = remotes[:maxRemoteWorkspaces]
	}
	if remotes == nil {
		remotes = []RemoteWorkspace{}
	}
	writeJSON(w, remotes)
}

func (a *App) handleRemoveRemote(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Key    string          `json:"key"`
		Remote RemoteWorkspace `json:"remote"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if request.Key == "" {
		remote := request.Remote.normalized()
		if remote.Host == "" || remote.Path == "" {
			http.Error(w, "remote key is required", http.StatusBadRequest)
			return
		}
		request.Key = remote.key()
	}

	s, err := loadSettings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RemoveRemote(request.Key)
	if err := saveSettings(s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.Remotes)
}

func (a *App) handleSaveRemote(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Remote   RemoteWorkspace `json:"remote"`
		Previous RemoteWorkspace `json:"previous,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request.Remote = request.Remote.normalized()
	if err := validateRemote(request.Remote); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request.Remote.Name = request.Remote.displayName()

	s, err := loadSettings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	previous := request.Previous.normalized()
	if previous.Host != "" && previous.Path != "" {
		s.RemoveRemote(previous.key())
	}
	s.AddRemote(request.Remote)
	if err := saveSettings(s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.Remotes)
}

func (a *App) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	s, err := loadSettings()

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	workspaces := s.Workspaces

	if len(workspaces) > maxWorkspaces {
		workspaces = workspaces[:maxWorkspaces]
	}

	writeJSON(w, workspaces)
}

func (a *App) handleRemoveWorkspace(w http.ResponseWriter, r *http.Request) {
	path, ok := readPath(w, r)

	if !ok {
		return
	}

	s, err := loadSettings()

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.RemoveWorkspace(path)

	if err := saveSettings(s); err != nil {
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

	if s, err := loadSettings(); err == nil {
		s.AddWorkspace(request.Path)
		_ = saveSettings(s)
	}

	w.WriteHeader(http.StatusNoContent)

	if current != nil {
		go current.Close()
	}
}

func (a *App) handleOpenRemote(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Remote   RemoteWorkspace `json:"remote"`
		Password string          `json:"password,omitempty"`
		Replace  bool            `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request.Remote = request.Remote.normalized()
	if err := validateRemote(request.Remote); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request.Remote.Name = request.Remote.displayName()

	a.mu.Lock()
	current := a.server
	if current != nil && !request.Replace {
		a.mu.Unlock()
		http.Error(w, "workspace already open", http.StatusConflict)
		return
	}
	a.mu.Unlock()

	settings, err := loadSettings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	starter := a.startRemote
	if starter == nil {
		starter = startRemoteWorkspace
	}
	srv, err := starter(request.Remote, settings, remoteCredentials{Password: request.Password})
	request.Password = ""
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	a.mu.Lock()
	if a.server != current {
		a.mu.Unlock()
		srv.Close()
		http.Error(w, "workspace changed while connecting", http.StatusConflict)
		return
	}
	a.server = srv
	a.mu.Unlock()

	settings.AddRemote(request.Remote)
	_ = saveSettings(settings)
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
