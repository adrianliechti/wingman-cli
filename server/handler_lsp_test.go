package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPDefinitionRequestValidation(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "invalid json", body: "{", status: http.StatusBadRequest},
		{name: "invalid position", body: `{"path":"main.go","line":0,"column":1}`, status: http.StatusBadRequest},
		{name: "invalid path", body: `{"path":"../main.go","line":1,"column":1}`, status: http.StatusBadRequest},
		{name: "missing file", body: `{"path":"missing.go","line":1,"column":1}`, status: http.StatusNotFound},
		{name: "no language server", body: `{"path":"main.go","content":"package main\n","line":1,"column":1}`, status: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := http.Post(web.URL+"/api/lsp/definition", "application/json", strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d (body %q)", response.StatusCode, test.status, body)
			}
		})
	}
}

func TestWorkspaceDiagnosticsResponseIncludesCoverage(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	app, err := New(context.Background(), t.TempDir(), &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	response, err := http.Get(web.URL + "/api/lsp/diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var body struct {
		Diagnostics        []json.RawMessage `json:"diagnostics"`
		CheckedFiles       int               `json:"checked_files"`
		DiscoveredFiles    int               `json:"discovered_files"`
		UnavailableServers []string          `json:"unavailable_servers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Diagnostics == nil || body.UnavailableServers == nil {
		t.Fatalf("array fields must not be null: %+v", body)
	}
	if body.CheckedFiles != 0 || body.DiscoveredFiles != 0 {
		t.Fatalf("unexpected coverage: %+v", body)
	}
}

func TestLSPFileDiagnosticsRequestValidation(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "invalid json", body: "{", status: http.StatusBadRequest},
		{name: "invalid path", body: `{"path":"../main.go"}`, status: http.StatusBadRequest},
		{name: "missing file", body: `{"path":"missing.go"}`, status: http.StatusNotFound},
		{name: "no language server", body: `{"path":"main.go"}`, status: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := http.Post(web.URL+"/api/lsp/diagnostics", "application/json", strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d (body %q)", response.StatusCode, test.status, body)
			}
		})
	}
}
