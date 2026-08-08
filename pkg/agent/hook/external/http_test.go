package external

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func withHTTPTransport(t *testing.T, transport roundTripFunc) {
	t.Helper()
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = previous })
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestHTTPUsesCodexPayloadAndStructuredResponse(t *testing.T) {
	var received map[string]any
	withHTTPTransport(t, func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		return response(http.StatusOK, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"remote policy"}}`), nil
	})

	cfg := configFor("PreToolUse", "Bash", Handler{Type: "http", URL: "https://hooks.test/pre"})
	outcome, err := cfg.Build(t.TempDir(), nil).PreToolUse[0](context.Background(), tool.ToolCall{ID: "call-http", Name: "shell", Args: `{"command":"ls"}`})
	if err != nil || !outcome.Block || outcome.Reason != "remote policy" {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
	if received["hook_event_name"] != "PreToolUse" || received["tool_name"] != "Bash" {
		t.Fatalf("received = %#v", received)
	}
}

func TestHTTPNon2xxIsNonBlocking(t *testing.T) {
	withHTTPTransport(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusForbidden, "failure"), nil
	})
	cfg := configFor("PreToolUse", "Bash", Handler{Type: "http", URL: "https://hooks.test/pre"})
	outcome, err := cfg.Build(t.TempDir(), nil).PreToolUse[0](context.Background(), tool.ToolCall{Name: "shell"})
	if err != nil || outcome.Block {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
}

func TestHTTPHeadersAreScopedToConfiguredOrigin(t *testing.T) {
	var requests []*http.Request
	withHTTPTransport(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request)
		if len(requests) == 1 {
			redirect := response(http.StatusTemporaryRedirect, "")
			redirect.Header.Set("Location", "https://other.test/hook")
			redirect.Request = request
			return redirect, nil
		}
		return response(http.StatusNoContent, ""), nil
	})
	cfg := configFor("PreToolUse", "Bash", Handler{
		Type:    "http",
		URL:     "https://hooks.test/pre",
		Headers: map[string]string{"Authorization": "plugin-secret", "X-Plugin": "demo"},
	})
	_, _ = cfg.Build(t.TempDir(), nil).PreToolUse[0](context.Background(), tool.ToolCall{Name: "shell"})
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want redirect followed", len(requests))
	}
	if requests[0].Header.Get("Authorization") != "plugin-secret" || requests[0].Header.Get("X-Plugin") != "demo" {
		t.Fatalf("configured-origin headers = %#v", requests[0].Header)
	}
	if requests[1].Header.Get("Authorization") != "" || requests[1].Header.Get("X-Plugin") != "" {
		t.Fatalf("cross-origin headers leaked: %#v", requests[1].Header)
	}
}

func TestMatchingHTTPHandlersRunConcurrently(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	withHTTPTransport(t, func(*http.Request) (*http.Response, error) {
		current := active.Add(1)
		for current > peak.Load() && !peak.CompareAndSwap(peak.Load(), current) {
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return response(http.StatusNoContent, ""), nil
	})

	cfg := &Config{Hooks: Events{PreToolUse: []MatcherGroup{{Matcher: "Bash", Hooks: []Handler{
		{Type: "http", URL: "https://hooks.test/first"},
		{Type: "http", URL: "https://hooks.test/second"},
	}}}}}
	done := make(chan struct{})
	workDir := t.TempDir()
	go func() {
		_, _ = cfg.Build(workDir, nil).PreToolUse[0](hook.WithRuntime(context.Background(), hook.Runtime{}), tool.ToolCall{Name: "shell"})
		close(done)
	}()

	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("matching handlers did not overlap")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent hook run did not finish")
	}
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak.Load())
	}
}
