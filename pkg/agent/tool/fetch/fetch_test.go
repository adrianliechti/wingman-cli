package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func fetchTool(t *testing.T) func(map[string]any) (string, error) {
	t.Helper()
	tl := Tools(nil, nil)[0]
	return func(args map[string]any) (string, error) {
		result, err := tl.Execute(t.Context(), args)
		return result.Content, err
	}
}

func TestFetchConvertsHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Docs</title><style>body{}</style><script>alert(1)</script></head>
<body><h1>Guide</h1><p>Hello <a href="/next">continue</a></p><ul><li>one</li><li>two</li></ul></body></html>`))
	}))
	defer server.Close()

	out, err := fetchTool(t)(map[string]any{"url": server.URL})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"# Guide", "[continue](/next)", "- one", "- two"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"alert(1)", "body{}", "<p>"} {
		if strings.Contains(out, banned) {
			t.Fatalf("output leaked %q:\n%s", banned, out)
		}
	}
}

func TestFetchPlainTextPassthrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	out, err := fetchTool(t)(map[string]any{"url": server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"ok":true}` {
		t.Fatalf("output = %q", out)
	}
}

func TestFetchRejectsBinaryAndBadInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte{0x00, 0x01, 0x02})
		case "/missing":
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	run := fetchTool(t)

	if _, err := run(map[string]any{"url": server.URL + "/bin"}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary fetch err = %v", err)
	}
	if _, err := run(map[string]any{"url": server.URL + "/missing"}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404 fetch err = %v", err)
	}
	if _, err := run(map[string]any{"url": "file:///etc/passwd"}); err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("scheme err = %v", err)
	}
	if _, err := run(map[string]any{}); err == nil {
		t.Fatal("missing url should error")
	}
}

func TestFetchTruncatesLongOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		for range 20000 {
			w.Write([]byte("some repeated line of text\n"))
		}
	}))
	defer server.Close()

	out, err := fetchTool(t)(map[string]any{"url": server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > maxOutputBytes+100 {
		t.Fatalf("output len = %d, want <= %d", len(out), maxOutputBytes+100)
	}
	if !strings.Contains(out, "[truncated at 48KB]") {
		t.Fatal("missing truncation notice")
	}
}

func TestFetchExtractsWithPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><p>The latest release is v2.7.1</p></body></html>`))
	}))
	defer server.Close()

	var gotInput string
	extract := func(_ context.Context, instructions, input string) (string, error) {
		gotInput = input
		return "v2.7.1", nil
	}
	tl := Tools(nil, extract)[0]

	result, err := tl.Execute(t.Context(), map[string]any{"url": server.URL, "prompt": "latest version?"})
	out := result.Content
	if err != nil {
		t.Fatal(err)
	}
	if out != "v2.7.1" {
		t.Fatalf("output = %q, want extracted answer", out)
	}
	for _, want := range []string{"latest version?", "The latest release is v2.7.1"} {
		if !strings.Contains(gotInput, want) {
			t.Fatalf("extract input missing %q:\n%s", want, gotInput)
		}
	}

	failing := func(_ context.Context, _, _ string) (string, error) {
		return "", context.DeadlineExceeded
	}
	tl = Tools(nil, failing)[0]
	result, err = tl.Execute(t.Context(), map[string]any{"url": server.URL, "prompt": "latest version?"})
	out = result.Content
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "The latest release is v2.7.1") {
		t.Fatalf("failed extraction should fall back to page text, got %q", out)
	}
}

func TestFetchHostApprovalGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	var prompts int
	allow := false
	elicit := &tool.Elicitation{Confirm: func(_ context.Context, _ string) (bool, error) {
		prompts++
		return allow, nil
	}}
	tl := Tools(elicit, nil)[0]

	if _, err := tl.Execute(t.Context(), map[string]any{"url": server.URL}); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("declined fetch err = %v", err)
	}

	allow = true
	if result, err := tl.Execute(t.Context(), map[string]any{"url": server.URL}); err != nil || result.Content != "ok" {
		t.Fatalf("approved fetch = %q, %v", result.Content, err)
	}
	if _, err := tl.Execute(t.Context(), map[string]any{"url": server.URL + "/again"}); err != nil {
		t.Fatalf("remembered approval fetch err = %v", err)
	}
	if prompts != 2 {
		t.Fatalf("prompts = %d, want 2 (decline + approve, then remembered)", prompts)
	}
}
