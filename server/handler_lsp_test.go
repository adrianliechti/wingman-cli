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
		{name: "keyword position without language server", body: `{"path":"main.go","content":"package main\n","line":1,"column":1}`, status: http.StatusOK},
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

func TestGraphFallbackNavigation(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	source := `package main

func main() {
	greet()
}

func greet() {}
`
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	post := func(t *testing.T, endpoint, body string, out any) {
		t.Helper()
		response, err := http.Post(web.URL+endpoint, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(response.Body)
			t.Fatalf("status = %d, body %q", response.StatusCode, data)
		}
		if err := json.NewDecoder(response.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("definition", func(t *testing.T) {
		var locations []lspLocationItem
		post(t, "/api/lsp/definition", `{"path":"main.go","line":4,"column":2}`, &locations)
		if len(locations) != 1 {
			t.Fatalf("locations = %+v, want one", locations)
		}
		if locations[0].Path != "main.go" || locations[0].Line != 7 || locations[0].Column != 6 {
			t.Fatalf("location = %+v, want main.go:7:6", locations[0])
		}
	})

	t.Run("references", func(t *testing.T) {
		var locations []lspLocationItem
		post(t, "/api/lsp/references", `{"path":"main.go","line":7,"column":6}`, &locations)
		if len(locations) != 2 {
			t.Fatalf("locations = %+v, want declaration and call site", locations)
		}
		if locations[0].Line != 4 || locations[1].Line != 7 {
			t.Fatalf("locations = %+v, want lines 4 and 7", locations)
		}
	})

	t.Run("document symbols", func(t *testing.T) {
		var symbols []struct {
			Name string `json:"name"`
			Kind int    `json:"kind"`
		}
		post(t, "/api/lsp/document-symbols", `{"path":"main.go"}`, &symbols)
		if len(symbols) != 2 || symbols[0].Name != "main" || symbols[1].Name != "greet" {
			t.Fatalf("symbols = %+v, want main and greet", symbols)
		}
		if symbols[0].Kind != 12 {
			t.Fatalf("kind = %d, want function", symbols[0].Kind)
		}
	})

	t.Run("dirty buffer symbols", func(t *testing.T) {
		var symbols []struct {
			Name string `json:"name"`
		}
		post(t, "/api/lsp/document-symbols", `{"path":"main.go","content":"package main\n\nfunc renamed() {}\n"}`, &symbols)
		if len(symbols) != 1 || symbols[0].Name != "renamed" {
			t.Fatalf("symbols = %+v, want renamed", symbols)
		}
	})

	t.Run("hover", func(t *testing.T) {
		var result struct {
			Contents string `json:"contents"`
		}
		post(t, "/api/lsp/hover", `{"path":"main.go","line":4,"column":2}`, &result)
		if !strings.Contains(result.Contents, "func greet()") || !strings.Contains(result.Contents, "main.go:7") {
			t.Fatalf("contents = %q, want snippet and location", result.Contents)
		}
	})

	t.Run("implementations empty for functions", func(t *testing.T) {
		var locations []lspLocationItem
		post(t, "/api/lsp/implementations", `{"path":"main.go","line":7,"column":6}`, &locations)
		if len(locations) != 0 {
			t.Fatalf("locations = %+v, want none", locations)
		}
	})

	t.Run("diagnostics stay gated", func(t *testing.T) {
		response, err := http.Post(web.URL+"/api/lsp/diagnostics", "application/json", strings.NewReader(`{"path":"main.go"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
		}
	})
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
