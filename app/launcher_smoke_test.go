package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestLauncherSmoke(t *testing.T) {
	testenv.UserHome(t)
	home := testenv.WingmanHome(t)
	t.Setenv("WINGMAN_URL", "https://example.com")
	t.Setenv("WINGMAN_TOKEN", "secret")

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
		t.Fatal("start page has no window drag region")
	} else if !strings.Contains(rec.Body.String(), `data-window-chrome="windows-overlay"`) {
		t.Fatal("start page has no app-owned Windows menu")
	} else if !strings.Contains(rec.Body.String(), "window.shell?.platform") {
		t.Fatal("start page does not consume the injected shell environment")
	} else if strings.Contains(rec.Body.String(), "shell_chrome") {
		t.Fatal("start page still depends on URL-based shell detection")
	}

	if rec := get("/app/workspaces"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "null" {
		t.Fatalf("workspaces: %d %q", rec.Code, rec.Body.String())
	}

	if rec := get("/app/settings"); rec.Code != http.StatusNotFound {
		t.Fatalf("removed settings endpoint: %d %q", rec.Code, rec.Body.String())
	}

	if rec := post("/app/workspaces/remove", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("remove without path: %d", rec.Code)
	}

	workspace := t.TempDir()

	if rec := post("/app/workspaces/open", `{"path":"`+workspace+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("open workspace: %d %q", rec.Code, rec.Body.String())
	}
	config, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if strings.Contains(string(config), "url") || strings.Contains(string(config), "token") || strings.Contains(string(config), "secret") {
		t.Fatalf("config contains connection credentials: %s", config)
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
