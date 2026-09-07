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

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestEditorCapabilityOptions(t *testing.T) {
	t.Run("rename", func(t *testing.T) {
		prepare := true
		tests := []struct {
			name        string
			provider    any
			wantEnabled bool
			wantPrepare bool
		}{
			{name: "missing"},
			{name: "disabled", provider: lsp.Boolean(false)},
			{name: "boolean", provider: lsp.Boolean(true), wantEnabled: true},
			{name: "options", provider: &lsp.RenameOptions{}, wantEnabled: true},
			{name: "prepare", provider: &lsp.RenameOptions{PrepareProvider: &prepare}, wantEnabled: true, wantPrepare: true},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				enabled, canPrepare := renameCapabilities(test.provider)
				if enabled != test.wantEnabled || canPrepare != test.wantPrepare {
					t.Fatalf("renameCapabilities() = (%v, %v), want (%v, %v)", enabled, canPrepare, test.wantEnabled, test.wantPrepare)
				}
			})
		}
	})

	t.Run("code action", func(t *testing.T) {
		resolve := true
		tests := []struct {
			name        string
			provider    any
			wantEnabled bool
			wantResolve bool
		}{
			{name: "missing"},
			{name: "disabled", provider: lsp.Boolean(false)},
			{name: "boolean", provider: lsp.Boolean(true), wantEnabled: true},
			{name: "options", provider: &lsp.CodeActionOptions{}, wantEnabled: true},
			{name: "resolve", provider: &lsp.CodeActionOptions{ResolveProvider: &resolve}, wantEnabled: true, wantResolve: true},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				enabled, canResolve := codeActionCapabilities(test.provider)
				if enabled != test.wantEnabled || canResolve != test.wantResolve {
					t.Fatalf("codeActionCapabilities() = (%v, %v), want (%v, %v)", enabled, canResolve, test.wantEnabled, test.wantResolve)
				}
			})
		}
	})
}

func TestLSPDefinitionRequestValidation(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(scopedTestHandler(app))
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

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(scopedTestHandler(app))
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
		var locations []lsp.Location
		post(t, "/api/lsp/definition", `{"path":"main.go","line":4,"column":2}`, &locations)
		if len(locations) != 1 {
			t.Fatalf("locations = %+v, want one", locations)
		}
		path, ok := fileuri.Path(string(locations[0].URI))
		if !ok || filepath.Clean(path) != filepath.Join(workDir, "main.go") || locations[0].Range.Start.Line != 6 || locations[0].Range.Start.Character != 5 {
			t.Fatalf("location = %+v, want main.go:7:6", locations[0])
		}
	})

	t.Run("references", func(t *testing.T) {
		var locations []lsp.Location
		post(t, "/api/lsp/references", `{"path":"main.go","line":7,"column":6}`, &locations)
		if len(locations) != 2 {
			t.Fatalf("locations = %+v, want declaration and call site", locations)
		}
		if locations[0].Range.Start.Line != 3 || locations[1].Range.Start.Line != 6 {
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

	t.Run("tree-sitter completions", func(t *testing.T) {
		var result struct {
			Items []struct {
				Label  string `json:"label"`
				Kind   int    `json:"kind"`
				Detail string `json:"detail"`
			} `json:"items"`
		}
		post(t, "/api/lsp/completions", `{"path":"main.go","line":4,"column":2}`, &result)
		items := result.Items
		if len(items) != 2 || items[0].Label != "main" || items[1].Label != "greet" {
			t.Fatalf("items = %+v, want main and greet", items)
		}
		if !strings.Contains(items[0].Detail, "tree-sitter") {
			t.Fatalf("detail = %q, want tree-sitter attribution", items[0].Detail)
		}
		if items[0].Kind != 3 {
			t.Fatalf("kind = %d, want function completion", items[0].Kind)
		}

		post(t, "/api/lsp/completions", `{"path":"main.go","line":4,"column":2,"trigger_kind":2,"trigger_character":"."}`, &result)
		if len(result.Items) != 0 {
			t.Fatalf("member fallback items = %+v, want none", result.Items)
		}
	})

	t.Run("signature help without language server", func(t *testing.T) {
		var help struct {
			Signatures []json.RawMessage `json:"signatures"`
		}
		post(t, "/api/lsp/signature-help", `{"path":"main.go","line":4,"column":2}`, &help)
		if help.Signatures == nil || len(help.Signatures) != 0 {
			t.Fatalf("signatures = %v, want empty array", help.Signatures)
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
		var locations []lsp.Location
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
	app, err := New(context.Background(), t.TempDir(), &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(scopedTestHandler(app))
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

func TestLSPStatusReturnsAnEmptyActivityList(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	app, err := New(context.Background(), t.TempDir(), &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	request := httptest.NewRequest(http.MethodGet, "/api/lsp/status", nil)
	response := httptest.NewRecorder()
	serveTestHTTP(app, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body lspActivityResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Analyzing || body.Services == nil || len(body.Services) != 0 {
		t.Fatalf("activity = %+v", body)
	}
}

func TestLSPFileDiagnosticsRequestValidation(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(scopedTestHandler(app))
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
