package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	} else if !strings.Contains(rec.Body.String(), "--wingman-window-drag: drag") {
		t.Fatal("start page has no macOS window drag region")
	}

	if rec := get("/app/workspaces"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "null" {
		t.Fatalf("workspaces: %d %q", rec.Code, rec.Body.String())
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

	if rec := get("/app/workspaces"); rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "/app/") {
		t.Fatalf("launcher still answering after open")
	}

	if rec := get("/api/capabilities"); rec.Code != http.StatusOK {
		t.Fatalf("workspace server not mounted: %d %q", rec.Code, rec.Body.String())
	}

	if rec := post("/app/workspaces/open", `{"path":"`+workspace+`"}`); rec.Code == http.StatusNoContent {
		t.Fatalf("second open should fail")
	}

	app.shutdown()
}
