package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestExternalGoIntelliSense(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}

	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module tmp\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "package main\n\nimport \"context\"\n\nfunc run(ctx context.Context) {\n\tctx.\n}\n"
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

	response, err := http.Post(web.URL+"/api/lsp/completions", "application/json", strings.NewReader(
		`{"path":"main.go","content":"package main\n\nimport \"context\"\n\nfunc run(ctx context.Context) {\n\tctx.\n}\n","line":6,"column":6,"trigger_kind":2,"trigger_character":"."}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("completion status = %d, body %q", response.StatusCode, body)
	}
	var result struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	items := result.Items
	labels := make(map[string]bool, len(items))
	for _, item := range items {
		labels[item.Label] = true
	}
	for _, want := range []string{"Deadline", "Done", "Err", "Value"} {
		if !labels[want] {
			t.Fatalf("completion labels = %v, missing %q", labels, want)
		}
	}

	signatureSource := "package main\n\nfunc consume(name string, count int) {}\n\nfunc run() {\n\tconsume(\n}\n"
	signatureBody, err := json.Marshal(map[string]any{
		"path":              "main.go",
		"content":           signatureSource,
		"line":              6,
		"column":            10,
		"trigger_kind":      2,
		"trigger_character": "(",
	})
	if err != nil {
		t.Fatal(err)
	}
	signatureResponse, err := http.Post(web.URL+"/api/lsp/signature-help", "application/json", strings.NewReader(string(signatureBody)))
	if err != nil {
		t.Fatal(err)
	}
	defer signatureResponse.Body.Close()
	if signatureResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(signatureResponse.Body)
		t.Fatalf("signature status = %d, body %q", signatureResponse.StatusCode, body)
	}
	var help struct {
		Signatures []struct {
			Label      string            `json:"label"`
			Parameters []json.RawMessage `json:"parameters"`
		} `json:"signatures"`
	}
	if err := json.NewDecoder(signatureResponse.Body).Decode(&help); err != nil {
		t.Fatal(err)
	}
	if len(help.Signatures) == 0 || !strings.Contains(help.Signatures[0].Label, "consume(name string, count int)") {
		t.Fatalf("signatures = %+v, want consume parameters", help.Signatures)
	}
	if len(help.Signatures[0].Parameters) != 2 {
		t.Fatalf("parameters = %s, want two", help.Signatures[0].Parameters)
	}
}

func TestExternalDefinitionNavigation(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}

	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module tmp\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	var locations []lsp.Location
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Post(web.URL+"/api/lsp/definition", "application/json",
			strings.NewReader(`{"path":"main.go","line":6,"column":6}`))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode == http.StatusOK {
			err = json.NewDecoder(response.Body).Decode(&locations)
			response.Body.Close()
			if err == nil && len(locations) > 0 {
				break
			}
		} else {
			response.Body.Close()
		}
		time.Sleep(time.Second)
	}
	if len(locations) == 0 {
		t.Fatal("no definition locations for fmt.Println")
	}

	loc := locations[0]
	t.Logf("location: %+v", loc)
	path, ok := fileuri.Path(string(loc.URI))
	if !ok {
		t.Fatalf("expected file URI, got %+v", loc)
	}
	if rel, err := filepath.Rel(workDir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("expected external location, got %+v", loc)
	}

	response, err := http.Get(web.URL + "/api/lsp/file?path=" + url.QueryEscape(filepath.ToSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("external file read = %d", response.StatusCode)
	}
	var file FileContent
	if err := json.NewDecoder(response.Body).Decode(&file); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file.Content, "func Println(") {
		t.Fatalf("external file %s does not contain Println definition", file.Path)
	}
	if file.Language != "go" {
		t.Fatalf("language = %q, want go", file.Language)
	}

	unlisted, err := http.Get(web.URL + "/api/lsp/file?path=" + url.QueryEscape("/etc/hosts"))
	if err != nil {
		t.Fatal(err)
	}
	unlisted.Body.Close()
	if unlisted.StatusCode != http.StatusNotFound {
		t.Fatalf("unlisted external path = %d, want 404", unlisted.StatusCode)
	}
}
