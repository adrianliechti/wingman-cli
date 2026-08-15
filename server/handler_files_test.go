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

	"github.com/adrianliechti/wingman-agent/pkg/code"
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

	res = postJSON("/api/files", map[string]string{
		"path":    "created-with-content.txt",
		"content": "draft contents\n",
	})
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("create with content status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	var created FileContent
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		res.Body.Close()
		t.Fatal(err)
	}
	res.Body.Close()
	if created.Path != "created-with-content.txt" || created.Content != "draft contents\n" || created.Revision == "" {
		t.Fatalf("create with content response = %#v", created)
	}
	if data, err := os.ReadFile(filepath.Join(workDir, "created-with-content.txt")); err != nil || string(data) != "draft contents\n" {
		t.Fatalf("created file with content = %q, %v", data, err)
	}

	res = postJSON("/api/files", map[string]any{
		"path":      "created-folder",
		"directory": true,
	})
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("create folder status = %d, want %d", res.StatusCode, http.StatusNoContent)
	}
	if info, err := os.Stat(filepath.Join(workDir, "created-folder")); err != nil || !info.IsDir() {
		t.Fatalf("created folder info = %#v, %v", info, err)
	}
	res = postJSON("/api/files", map[string]string{"path": "created-folder/nested.txt"})
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("nested create status = %d, want %d", res.StatusCode, http.StatusNoContent)
	}
	res = postJSON("/api/files", map[string]any{
		"path":      "created-folder",
		"directory": true,
	})
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate folder status = %d, want %d", res.StatusCode, http.StatusConflict)
	}

	res, err = http.Get(web.URL + "/api/files/path?path=created-folder%2Fnested.txt")
	if err != nil {
		t.Fatal(err)
	}
	var paths struct {
		Path     string `json:"path"`
		Relative string `json:"relative"`
	}
	if err := json.NewDecoder(res.Body).Decode(&paths); err != nil {
		res.Body.Close()
		t.Fatal(err)
	}
	res.Body.Close()
	if paths.Path != filepath.Join(workDir, "created-folder", "nested.txt") || paths.Relative != "created-folder/nested.txt" {
		t.Fatalf("file paths = %#v", paths)
	}

	res = postJSON("/api/files/rename", map[string]string{
		"from": "missing.txt",
		"to":   "moved.txt",
	})
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing rename status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
	res = postJSON("/api/files/rename", map[string]string{
		"from": "created-folder",
		"to":   "created-folder/inside",
	})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("recursive rename status = %d, want %d", res.StatusCode, http.StatusBadRequest)
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

func TestFileBatchWriteChecksEveryRevisionBeforeWriting(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	for name, content := range map[string]string{
		"first.txt":  "first\n",
		"second.txt": "second\n",
	} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	post := func(files []fileBatchWrite) *http.Response {
		t.Helper()
		data, err := json.Marshal(map[string]any{"files": files})
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(web.URL+"/api/files/write-batch", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	response := post([]fileBatchWrite{
		{Path: "first.txt", Content: "one\n", Revision: fileRevision([]byte("first\n"))},
		{Path: "second.txt", Content: "two\n", Revision: fileRevision([]byte("second\n"))},
	})
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("batch write status = %d, body = %q", response.StatusCode, body)
	}
	var result struct {
		Revisions map[string]string `json:"revisions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if result.Revisions["first.txt"] != fileRevision([]byte("one\n")) || result.Revisions["second.txt"] != fileRevision([]byte("two\n")) {
		t.Fatalf("batch revisions = %#v", result.Revisions)
	}

	if err := os.WriteFile(filepath.Join(workDir, "second.txt"), []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	response = post([]fileBatchWrite{
		{Path: "first.txt", Content: "must not write\n", Revision: result.Revisions["first.txt"]},
		{Path: "second.txt", Content: "must not write\n", Revision: result.Revisions["second.txt"]},
	})
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale batch status = %d, want %d", response.StatusCode, http.StatusConflict)
	}
	for name, want := range map[string]string{
		"first.txt":  "one\n",
		"second.txt": "external\n",
	} {
		content, err := os.ReadFile(filepath.Join(workDir, name))
		if err != nil || string(content) != want {
			t.Fatalf("%s after rejected batch = %q, %v; want %q", name, content, err, want)
		}
	}
}

func TestWorkspaceContentSearchStreamsFilteredReplaceableMatches(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		".gitignore":           "ignored.txt\n",
		"notes.txt":            "Alpha alpha cat scatter 😀cat\n",
		"ignored.txt":          "cat\n",
		"src/code.go":          "const cat = cat\n",
		"build/out.txt":        "cat\n",
		"nested/.gitignore":    "skipped.txt\n",
		"nested/skipped.txt":   "cat\n",
		"nested/not-text.data": "cat\n",
	} {
		if err := os.WriteFile(filepath.Join(workDir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	root, err := os.OpenRoot(workDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	app := &Server{workspace: &code.Workspace{Root: root, RootPath: workDir}}

	body, err := json.Marshal(workspaceSearchRequest{
		Query:         "cat",
		Replacement:   "dog",
		CaseSensitive: true,
		WholeWord:     true,
		Include:       "*.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/files/content-search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	app.handleWorkspaceSearch(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(response.Body)
		t.Fatalf("search status = %d, body = %q", response.StatusCode, detail)
	}

	decoder := json.NewDecoder(response.Body)
	var fileEvent workspaceSearchEvent
	if err := decoder.Decode(&fileEvent); err != nil {
		t.Fatal(err)
	}
	if fileEvent.Type != "file" || fileEvent.File == nil || fileEvent.File.Path != "notes.txt" {
		t.Fatalf("file event = %#v", fileEvent)
	}
	if fileEvent.File.Revision == "" || len(fileEvent.File.Matches) != 2 {
		t.Fatalf("search file = %#v", fileEvent.File)
	}
	first, second := fileEvent.File.Matches[0], fileEvent.File.Matches[1]
	if first.Line != 1 || first.Column != 13 || first.EndColumn != 16 || first.Replacement != "dog" {
		t.Fatalf("first match = %#v", first)
	}
	if second.Column != 27 || second.EndColumn != 30 {
		t.Fatalf("unicode match columns = %#v", second)
	}

	var done workspaceSearchEvent
	if err := decoder.Decode(&done); err != nil {
		t.Fatal(err)
	}
	if done.Type != "done" || done.Files != 1 || done.Matches != 2 || done.Truncated {
		t.Fatalf("done event = %#v", done)
	}
}

func TestWorkspaceContentSearchRegexReplacementAndValidation(t *testing.T) {
	config, err := newWorkspaceSearchConfig(workspaceSearchRequest{
		Query:         "(cat)",
		Replacement:   "${1}s",
		Regex:         true,
		CaseSensitive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	matches, more := config.searchFile("cat scatter cat", 10)
	if more || len(matches) != 3 {
		t.Fatalf("matches = %#v, more = %v", matches, more)
	}
	if matches[0].Replacement != "cats" {
		t.Fatalf("expanded replacement = %q", matches[0].Replacement)
	}

	if _, err := newWorkspaceSearchConfig(workspaceSearchRequest{Query: "(", Regex: true}); err == nil {
		t.Fatal("invalid regex was accepted")
	}
	if _, err := newWorkspaceSearchConfig(workspaceSearchRequest{Query: "cat", Include: "["}); err == nil {
		t.Fatal("invalid glob was accepted")
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
