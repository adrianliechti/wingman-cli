package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	var locations []lspLocationItem
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
	if !loc.External {
		t.Fatalf("expected external location, got %+v", loc)
	}

	response, err := http.Get(web.URL + "/api/lsp/file?path=" + url.QueryEscape(loc.Path))
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
