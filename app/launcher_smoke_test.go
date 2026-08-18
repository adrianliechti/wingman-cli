package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubWorkspaceServer struct {
	closed bool
}

func (s *stubWorkspaceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/capabilities" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"remote":true}`))
		return
	}
	http.NotFound(w, r)
}

func (s *stubWorkspaceServer) Close() {
	s.closed = true
}

func TestLauncherSmoke(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	app := &App{}
	app.launcher = app.newLauncher()

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	if rec := get("/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Wingman Agent") {
		t.Fatalf("start page: %d", rec.Code)
	} else if !strings.Contains(rec.Body.String(), "--shell-window-drag: drag") {
		t.Fatal("start page has no macOS window drag region")
	} else if !strings.Contains(rec.Body.String(), "Add SSH") {
		t.Fatal("start page has no remote workspace action")
	}

	if rec := get("/app/workspaces"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "null" {
		t.Fatalf("workspaces: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/app/remotes"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("remotes: %d %q", rec.Code, rec.Body.String())
	}

	if rec := post("/app/settings", `{"url":"https://example.com","token":"secret"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("save settings: %d %q", rec.Code, rec.Body.String())
	}

	if rec := get("/app/settings"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://example.com") {
		t.Fatalf("settings: %d %q", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(home, ".wingman", "config.json")); err != nil {
		t.Fatalf("config not written: %v", err)
	}

	if rec := post("/app/workspaces/remove", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("remove without path: %d", rec.Code)
	}

	workspace := t.TempDir()

	if rec := post("/app/workspaces/open", `{"path":"`+workspace+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("open workspace: %d %q", rec.Code, rec.Body.String())
	}

	if rec := get("/app/workspaces"); rec.Code != http.StatusOK {
		t.Fatalf("app commands unavailable after open: %d", rec.Code)
	}

	if rec := get("/api/capabilities"); rec.Code != http.StatusOK {
		t.Fatalf("workspace server not mounted: %d %q", rec.Code, rec.Body.String())
	}

	if rec := post("/app/workspaces/open", `{"path":"`+workspace+`"}`); rec.Code == http.StatusNoContent {
		t.Fatalf("second open should fail")
	}

	replacement := t.TempDir()
	if rec := post("/app/workspaces/open", `{"path":"`+replacement+`","replace":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("replace workspace: %d %q", rec.Code, rec.Body.String())
	}

	app.shutdown()
}

func TestLauncherOpensRemoteWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	stub := &stubWorkspaceServer{}
	starts := 0
	app := &App{
		startRemote: func(remote RemoteWorkspace, settings Settings, credentials remoteCredentials) (workspaceServer, error) {
			starts++
			if remote.Host != "devbox" || remote.Path != "/srv/project" {
				t.Fatalf("unexpected remote: %+v", remote)
			}
			if credentials.Password != "one-time-secret" {
				t.Fatal("unexpected SSH credentials")
			}
			return stub, nil
		},
	}
	app.launcher = app.newLauncher()

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	profile := `{"remote":{"kind":"ssh","name":"Old","host":"devbox","path":"/srv/old"}}`
	if rec := post("/app/remotes/save", profile); rec.Code != http.StatusOK {
		t.Fatalf("save remote: %d %q", rec.Code, rec.Body.String())
	}
	if starts != 0 {
		t.Fatal("saving a remote unexpectedly connected to it")
	}
	edit := `{"remote":{"kind":"ssh","name":"Project","host":"devbox","path":"/srv/project"},"previous":{"kind":"ssh","name":"Old","host":"devbox","path":"/srv/old"}}`
	if rec := post("/app/remotes/save", edit); rec.Code != http.StatusOK {
		t.Fatalf("edit remote: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/app/remotes"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"/srv/project"`) {
		t.Fatalf("remote not saved: %d %q", rec.Code, rec.Body.String())
	} else if strings.Contains(rec.Body.String(), `"path":"/srv/old"`) {
		t.Fatalf("old remote profile was not replaced: %q", rec.Body.String())
	} else if strings.Contains(rec.Body.String(), "password") || strings.Contains(rec.Body.String(), "one-time-secret") {
		t.Fatal("remote profile contains authentication state")
	}

	connection := `{"remote":{"kind":"ssh","name":"Project","host":"devbox","path":"/srv/project"},"password":"one-time-secret"}`
	if rec := post("/app/remotes/open", connection); rec.Code != http.StatusNoContent {
		t.Fatalf("open remote: %d %q", rec.Code, rec.Body.String())
	}
	if starts != 1 {
		t.Fatalf("remote starts = %d, want 1", starts)
	}
	if rec := get("/api/capabilities"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"remote":true`) {
		t.Fatalf("remote server not mounted: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/app/remotes"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"host":"devbox"`) {
		t.Fatalf("remote not saved: %d %q", rec.Code, rec.Body.String())
	} else if strings.Contains(rec.Body.String(), "one-time-secret") || strings.Contains(rec.Body.String(), "password") {
		t.Fatal("SSH password was saved in the remote profile")
	}

	app.shutdown()
	if !stub.closed {
		t.Fatal("remote server was not closed")
	}
}
