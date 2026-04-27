package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/server"
)

//go:embed all:public
var publicFS embed.FS

type App struct {
	ctx     context.Context
	agent   *code.Agent
	handler atomic.Pointer[http.Handler]
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	if s, err := loadSettings(); err == nil {
		s.Apply()
	}
}

func (a *App) GetSettings() (Settings, error) {
	return loadSettings()
}

func (a *App) SaveSettings(s Settings) error {
	if err := saveSettings(s); err != nil {
		return err
	}

	s.Apply()
	return nil
}

func (a *App) shutdown(ctx context.Context) {
	if a.agent != nil {
		a.agent.Close()
	}
}

func (a *App) SelectFolder() (string, error) {
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Open Workspace",
	})
}

func (a *App) OpenWorkspace(path string) error {
	if path == "" {
		return errors.New("path is required")
	}

	c, err := code.New(path, nil)
	if err != nil {
		return err
	}

	s := server.New(a.ctx, c, 0)

	a.agent = c

	// Wails' AssetServer doesn't support http.Hijacker, so WebSocket upgrades
	// fail through it. Run a real TCP listener for the same handler and tell
	// the server to advertise that URL via /api/ws. Plain HTTP/API keeps
	// flowing through the AssetServer so cookies/origin stay on
	// wails.localhost.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	go http.Serve(listener, s)
	s.SetWebSocketURL(fmt.Sprintf("ws://%s/ws", listener.Addr().String()))

	var h http.Handler = s
	a.handler.Store(&h)

	return nil
}

func (a *App) assetHandler() http.Handler {
	startFS, _ := fs.Sub(publicFS, "public")
	startServer := http.FileServer(http.FS(startFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hp := a.handler.Load(); hp != nil {
			(*hp).ServeHTTP(w, r)
			return
		}

		startServer.ServeHTTP(w, r)
	})
}

func main() {
	app := &App{}

	opts := &options.App{
		Title: "Wingman Agent",

		Width:  1280,
		Height: 768,

		OnStartup:  app.startup,
		OnShutdown: app.shutdown,

		Bind: []any{app},

		AssetServer: &assetserver.Options{
			Handler: app.assetHandler(),
		},
	}

	if err := wails.Run(opts); err != nil {
		panic(err)
	}
}
