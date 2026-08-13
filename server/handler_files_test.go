package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileCreateAndConflictAwareWrite(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	filePath := filepath.Join(workDir, "editable.txt")
	if err := os.WriteFile(filePath, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	postJSON := func(endpoint string, body any) *http.Response {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.Post(web.URL+endpoint, "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	res := postJSON("/api/files", map[string]string{"path": "created.txt"})
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("create status = %d, want %d", res.StatusCode, http.StatusNoContent)
	}
	if data, err := os.ReadFile(filepath.Join(workDir, "created.txt")); err != nil || len(data) != 0 {
		t.Fatalf("created file = %q, %v; want empty file", data, err)
	}
	res = postJSON("/api/files", map[string]string{"path": "created.txt"})
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, want %d", res.StatusCode, http.StatusConflict)
	}

	res, err = http.Get(web.URL + "/api/files/read?path=editable.txt")
	if err != nil {
		t.Fatal(err)
	}
	var original FileContent
	if err := json.NewDecoder(res.Body).Decode(&original); err != nil {
		res.Body.Close()
		t.Fatal(err)
	}
	res.Body.Close()
	if original.Revision == "" {
		t.Fatal("read response has no revision")
	}

	if err := os.WriteFile(filePath, []byte("changed elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = postJSON("/api/files/write", map[string]any{
		"path":     "editable.txt",
		"content":  "my edit\n",
		"revision": original.Revision,
	})
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("stale write status = %d, want %d", res.StatusCode, http.StatusConflict)
	}
	if data, err := os.ReadFile(filePath); err != nil || string(data) != "changed elsewhere\n" {
		t.Fatalf("file after stale write = %q, %v", data, err)
	}

	res = postJSON("/api/files/write", map[string]any{
		"path":    "editable.txt",
		"content": "my edit\n",
		"force":   true,
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("forced write status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	var saved struct {
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(res.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision == "" || saved.Revision == original.Revision {
		t.Fatalf("forced write revision = %q, original = %q", saved.Revision, original.Revision)
	}
}

func TestFilePreviewServesWebsiteAssetsInline(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	siteDir := filepath.Join(workDir, "site")
	if err := os.MkdirAll(filepath.Join(siteDir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.html":      `<!doctype html><link rel="stylesheet" href="style.css"><img src="images/logo.svg">`,
		"style.css":       "body { color: rebeccapurple; }",
		"images/logo.svg": `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(siteDir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workDir, "secret.txt"), []byte("outside preview root"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	tests := []struct {
		path        string
		contentType string
		body        string
	}{
		{"/api/files/preview?path=site%2Findex.html", "text/html", "<!doctype html>"},
		{"/style.css", "text/css", "rebeccapurple"},
		{"/images/logo.svg", "image/svg+xml", "<svg"},
	}
	for i, test := range tests {
		requestURL := app.preview.URL() + test.path
		if i == 0 {
			requestURL = web.URL + test.path
		}
		res, err := client.Get(requestURL)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %q", test.path, res.StatusCode, body)
		}
		if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, test.contentType) {
			t.Errorf("%s: content type = %q, want prefix %q", test.path, got, test.contentType)
		}
		if got := res.Header.Get("Content-Disposition"); got != "" {
			t.Errorf("%s: content disposition = %q, want inline response", test.path, got)
		}
		if !strings.Contains(string(body), test.body) {
			t.Errorf("%s: body = %q, want it to contain %q", test.path, body, test.body)
		}
	}

	res, err := client.Get(app.preview.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("directory index status = %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Security-Policy"); !strings.Contains(got, "form-action 'none'") {
		t.Fatalf("content security policy = %q", got)
	}

	res, err = client.Get(app.preview.URL() + "/%2e%2e%5csecret.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(res.Body)
	res.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if res.StatusCode != http.StatusNotFound || strings.Contains(string(body), "outside preview root") {
		t.Fatalf("preview root escape: status = %d, body = %q", res.StatusCode, body)
	}
}
